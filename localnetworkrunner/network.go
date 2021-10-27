package localnetworkrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	ps "github.com/mitchellh/go-ps"
	"github.com/palantir/stacktrace"
	"github.com/sirupsen/logrus"
)

// Network keeps information uses for network management, and accessing all the nodes
type Network struct {
	binMap          map[int]string         // map node kind to string used in node set up (binary path)
	nextIntNodeID   uint64                 // next id to be used in internal node id generation
	nodes           map[ids.ID]*Node       // access to nodes basic info and api
	procs           map[ids.ID]*exec.Cmd   // access to the nodes OS processes
	coreConfigFlags map[string]interface{} // cmdline flags common to all nodes
	genesis         []byte                 // genesis file contents, common to all nodes
	cChainConfig    []byte                 // cchain config file contents, common to all nodes
	log             *logrus.Logger         // logger used by the library
}

// interface compliance
var _ networkrunner.Network = (*Network)(nil)

func ParseJSON() {
}

// NewNetwork creates a network from given configuration and map of node kinds to binaries
func NewNetwork(networkConfig networkrunner.NetworkConfig, binMap map[int]string, log *logrus.Logger) (*Network, error) {
	net := Network{}
	net.nodes = map[ids.ID]*Node{}
	net.procs = map[ids.ID]*exec.Cmd{}

	net.nextIntNodeID = 1
	net.binMap = binMap
	net.log = log
	net.genesis = []byte(networkConfig.Genesis)
	net.cChainConfig = []byte(networkConfig.CChainConfig)
	if err := json.Unmarshal([]byte(networkConfig.CoreConfigFlags), &net.coreConfigFlags); err != nil {
		return nil, err
	}

	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.AddNode(nodeConfig)
		if err != nil {
			return nil, err
		}
	}

	return &net, nil
}

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
func (net *Network) AddNode(nodeConfig networkrunner.NodeConfig) (networkrunner.Node, error) {
	var configFlags map[string]interface{} = make(map[string]interface{})

	// copy common config flags, and unmarshall specific node config flags
	for k, v := range net.coreConfigFlags {
		configFlags[k] = v
	}
	if err := json.Unmarshal([]byte(nodeConfig.ConfigFlags), &configFlags); err != nil {
		return nil, err
	}

	configDir, ok := configFlags[config.ChainConfigDirKey].(string)
	if !ok {
		return nil, errors.New(fmt.Sprintf("%s flag node %v", config.ChainConfigDirKey, net.nextIntNodeID))
	}

	// create main config file by marshalling all config flags
	configFilePath := path.Join(configDir, "config.json")
	configBytes, err := json.Marshal(configFlags)
	if err != nil {
		return nil, err
	}
	if err := createFile(configFilePath, configBytes); err != nil {
		return nil, err
	}

	cConfigFilePath := path.Join(configDir, "C", "config.json")
	if err := createFile(cConfigFilePath, net.cChainConfig); err != nil {
		return nil, err
	}

	genesisFname, ok := configFlags[config.GenesisConfigFileKey].(string)
	if !ok {
		return nil, errors.New(fmt.Sprintf("%s flag node %v", config.GenesisConfigFileKey, net.nextIntNodeID))
	}
	if err := createFile(genesisFname, net.genesis); err != nil {
		return nil, err
	}

	// tells if avalanchego is going to generate the cert/key for the node
	usesEphemeralCert, ok := configFlags[config.StakingEphemeralCertEnabledKey].(bool)
	if !ok {
		usesEphemeralCert = false
	}

	if !usesEphemeralCert {
		// cert/key is given in config
		certFname, ok := configFlags[config.StakingCertPathKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.StakingCertPathKey, net.nextIntNodeID))
		}
		if err := createFile(certFname, []byte(nodeConfig.Cert)); err != nil {
			return nil, err
		}
		keyFname, ok := configFlags[config.StakingKeyPathKey].(string)
		if !ok {
			return nil, errors.New(fmt.Sprintf("%s flag node %v", config.StakingKeyPathKey, net.nextIntNodeID))
		}
		if err := createFile(keyFname, []byte(nodeConfig.PrivateKey)); err != nil {
			return nil, err
		}
	}

	// get binary from bin map and node kind, and execute it
	avalanchegoPath := net.binMap[nodeConfig.BinKind]
	configFileFlag := fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath)
	cmd := exec.Command(avalanchegoPath, configFileFlag)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// initialize node avalanchego apis
	nodeIP, ok := configFlags[config.PublicIPKey].(string)
	if !ok {
		return nil, errors.New(fmt.Sprintf("%s flag node %v", config.PublicIPKey, net.nextIntNodeID))
	}
	nodePortF, ok := configFlags[config.HTTPPortKey].(float64)
	if !ok {
		return nil, errors.New(fmt.Sprintf("%s flag node %v", config.HTTPPortKey, net.nextIntNodeID))
	}
	nodePort := uint(nodePortF)
	apiClient := NewAPIClient(nodeIP, nodePort, 20*time.Second)

	// get internal node id from incremental uint
	// done in this way so the id when printed or converted, is correctly interpreted
	b := big.NewInt(0).SetUint64(net.nextIntNodeID).Bytes()
	nodeID := ids.ID{}
	copy(nodeID[len(nodeID)-len(b):], b)
	net.nextIntNodeID += 1

	node := Node{id: nodeID, client: *apiClient}

	net.nodes[nodeID] = &node
	net.procs[nodeID] = cmd

	return node, nil
}

// Ready closes readyCh is the network has initialized, or send error indicating network will not initialize
func (net *Network) Ready() (chan struct{}, chan error) {
	readyCh := make(chan struct{})
	errorCh := make(chan error)
	go func() {
		for id := range net.nodes {
			intID := big.NewInt(0).SetBytes(id[:])
			b := waitNode(net.nodes[id].client)
			if !b {
				errorCh <- errors.New(fmt.Sprintf("timeout waiting for node %v", intID))
			}
			net.log.Infof("node %v is up", intID)
		}
		close(readyCh)
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

func (net *Network) GetNodesIDs() []ids.ID {
	ks := make([]ids.ID, 0, len(net.nodes))
	for k := range net.nodes {
		ks = append(ks, k)
	}
	return ks
}

func (net *Network) Stop() error {
	for nodeID := range net.nodes {
		if err := net.RemoveNode(nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (net *Network) RemoveNode(nodeID ids.ID) error {
	node, ok := net.nodes[nodeID]
	if !ok {
		return errors.New(fmt.Sprintf("node %s not found in network", nodeID))
	}
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	proc, ok := net.procs[nodeID]
	if !ok {
		return errors.New(fmt.Sprintf("node %s not found in network", nodeID))
	}
	processes, err := ps.Processes()
	if err != nil {
		return stacktrace.Propagate(err, "unable to list processes")
	}
	procID := proc.Process.Pid
	if err := killProcessAndDescendants(procID, processes); err != nil {
		return err
	}
	delete(net.nodes, nodeID)
	delete(net.procs, nodeID)
	return nil
}

// createFile creates a file under the fiven fname, making all
// intermediate dirs, and filling it with contents
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

// waitNode waits until the node initializes, or timeouts
func waitNode(client APIClient) bool {
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
