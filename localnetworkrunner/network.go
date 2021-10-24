package localnetworkrunner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"

	oldnetworkrunner "github.com/ava-labs/avalanche-testing/avalanche/builder/networkrunner"
	"github.com/ava-labs/avalanche-testing/avalanche/libs/avalanchegoclient"
	"github.com/ava-labs/avalanche-testing/avalanche/networkrunner"
	"github.com/ava-labs/avalanche-testing/logging"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	ps "github.com/mitchellh/go-ps"
	"github.com/palantir/stacktrace"
)

type Network struct {
	procs map[ids.ID]*exec.Cmd
	nodes map[ids.ID]*Node
}

func NewNetwork(networkConfig networkrunner.NetworkConfig, binMap map[int]string) (*Network, error) {
	net := Network{}
	net.procs = map[ids.ID]*exec.Cmd{}
	net.nodes = map[ids.ID]*Node{}

	var coreConfigFlags map[string]interface{}
	if err := json.Unmarshal(networkConfig.CoreConfigFlags, &coreConfigFlags); err != nil {
		return nil, err
	}

	var intNodeID uint64 = 1
	for _, nodeConfig := range networkConfig.NodeConfigs {
		var configFlags map[string]interface{} = make(map[string]interface{})
		for k, v := range coreConfigFlags {
			configFlags[k] = v
		}
		if err := json.Unmarshal(nodeConfig.ConfigFlags, &configFlags); err != nil {
			return nil, err
		}

		configBytes, err := json.Marshal(configFlags)
		if err != nil {
			return nil, err
		}

		configDir, ok := configFlags[config.ChainConfigDirKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.ChainConfigDirKey, intNodeID))
		}
		configFilePath := path.Join(configDir, "config.json")
		cConfigFilePath := path.Join(configDir, "C", "config.json")

		genesisFname, ok := configFlags[config.GenesisConfigFileKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.GenesisConfigFileKey, intNodeID))
		}
		if err := createFile(genesisFname, networkConfig.Genesis); err != nil {
			return nil, err
		}

		if err := createFile(cConfigFilePath, networkConfig.CChainConfig); err != nil {
			return nil, err
		}

		certFname, ok := configFlags[config.StakingCertPathKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.StakingCertPathKey, intNodeID))
		}
		if err := createFile(certFname, nodeConfig.Cert); err != nil {
			return nil, err
		}

		keyFname, ok := configFlags[config.StakingKeyPathKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.StakingKeyPathKey, intNodeID))
		}
		if err := createFile(keyFname, nodeConfig.PrivateKey); err != nil {
			return nil, err
		}

		if err := createFile(configFilePath, configBytes); err != nil {
			return nil, err
		}

		ch := make(chan string, 1)
		read, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() {
			sc := bufio.NewScanner(read)
			for sc.Scan() {
				logging.Debugf("[%v] - %s\n", intNodeID, sc.Text())
			}
			close(ch)
		}()

		avalanchegoPath := binMap[nodeConfig.BinKind]
		configFileFlag := fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath)
		cmd := exec.Command(avalanchegoPath, configFileFlag)

		cmd.Stdout = w
		cmd.Stderr = w
		if err := cmd.Start(); err != nil {
			return nil, err
		}

		nodeIP, ok := configFlags[config.PublicIPKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.PublicIPKey, intNodeID))
		}
		nodePortF, ok := configFlags[config.HTTPPortKey].(float64)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.HTTPPortKey, intNodeID))
		}
		nodePort := uint(nodePortF)

		nodeRunner, _ := oldnetworkrunner.NewNodeRunnerFromFields(
			string(intNodeID),
			string(intNodeID),
			nodeIP,
			nodePort,
			avalanchegoclient.NewClient(nodeIP, nodePort, nodePort, 20*time.Second),
		)

		b := big.NewInt(0).SetUint64(intNodeID).Bytes()
		nodeID := ids.ID{}
		copy(nodeID[32-len(b):], b)

		net.procs[nodeID] = cmd
		net.nodes[nodeID] = &Node{id: nodeID, client: APIClient{nodeRunner}}

		intNodeID += 1
	}

	return &net, nil
}

func (net *Network) Ready() (chan struct{}, chan error) {
	readyCh := make(chan struct{})
	errorCh := make(chan error)
	go func() {
		for id := range net.nodes {
			intID := big.NewInt(0).SetBytes(id[:])
			b := waitNode(net.nodes[id].client.runner.GetClient())
			if !b {
				errorCh <- errors.New(fmt.Sprintf("timeout waiting for node %v", intID))
			}
			logging.Infof("node %v is up", intID)
		}
		readyCh <- struct{}{}
	}()
	return readyCh, errorCh
}

func (net *Network) GetNode(nodeID ids.ID) (networkrunner.Node, error) {
	node, ok := net.nodes[nodeID]
	if !ok {
		return nil, errors.New(fmt.Sprintf("node %s not found in network", nodeID))
	}
	return node, nil
}

func (net *Network) Stop() error {
	processes, err := ps.Processes()
	if err != nil {
		return stacktrace.Propagate(err, "unable to list processes")
	}
	for _, proc := range net.procs {
		procID := proc.Process.Pid
		if err := killProcessAndDescendants(procID, processes); err != nil {
			return err
		}
	}
	return nil
}

func createFile(fname string, contents []byte) error {
	if err := os.MkdirAll(path.Dir(fname), 0o750); err != nil {
		return err
	}
	file, err := os.Create(fname)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		return err
	}
	file.Close()
	return nil
}

func waitNode(client *avalanchegoclient.Client) bool {
	info := client.InfoAPI()
	timeout := 1 * time.Minute
	pollTime := 10 * time.Second
	nodeIsUp := false
	for t0 := time.Now(); !nodeIsUp && time.Since(t0) <= timeout; {
		nodeIsUp = true
		if bootstrapped, err := info.IsBootstrapped("P"); err != nil || !bootstrapped {
			nodeIsUp = false
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("C"); err != nil || !bootstrapped {
			nodeIsUp = false
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("X"); err != nil || !bootstrapped {
			nodeIsUp = false
		}
		if !nodeIsUp {
			time.Sleep(pollTime)
		}
	}
	return nodeIsUp
}

func killProcessAndDescendants(processID int, processes []ps.Process) error {
	// Kill descendants of [processID] in [processes]
	for _, process := range processes {
		if process.PPid() != processID {
			continue
		}
		if err := killProcessAndDescendants(process.Pid(), processes); err != nil {
			return stacktrace.Propagate(err, "unable to kill process and descendants")
		}
	}
	// Kill [processID]
	return syscall.Kill(processID, syscall.SIGTERM)
}
