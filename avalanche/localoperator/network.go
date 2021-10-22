package localoperator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-testing/avalanche/libs/avalanchegoclient"
	"github.com/ava-labs/avalanche-testing/logging"
	"github.com/ava-labs/avalanchego/config"
	ps "github.com/mitchellh/go-ps"
	"github.com/palantir/stacktrace"
)

type Network struct {
	procs   map[string]*exec.Cmd
	clients map[string]*avalanchegoclient.Client
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

func NewNetwork(networkConfig NetworkConfig, binMap map[int]string) (*Network, error) {
	net := Network{}
	net.procs = map[string]*exec.Cmd{}
	net.clients = map[string]*avalanchegoclient.Client{}

	var configFlags map[string]interface{}
	if err := json.Unmarshal(networkConfig.CoreConfigFlags, &configFlags); err != nil {
		return nil, err
	}

	for _, nodeConfig := range networkConfig.NodeConfigs {
		if err := json.Unmarshal(nodeConfig.ConfigFlags, &configFlags); err != nil {
			return nil, err
		}

		configDir := configFlags["chain-config-dir"].(string)
		createFile(configFlags["genesis"].(string), networkConfig.Genesis)
		createFile(path.Join(configDir, "C", "config.json"), networkConfig.CChainConfig)
		createFile(configFlags["staking-tls-cert-file"].(string), nodeConfig.Cert)
		createFile(configFlags["staking-tls-key-file"].(string), nodeConfig.PrivateKey)
		configBytes, err := json.Marshal(configFlags)
		if err != nil {
			return nil, err
		}
		configFilePath := path.Join(configDir, "config.json")
		createFile(configFilePath, configBytes)

		ch := make(chan string, 1)
		read, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() {
			sc := bufio.NewScanner(read)
			for sc.Scan() {
				logging.Debugf("[%s] - %s\n", nodeConfig.NodeID, sc.Text())
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

		net.procs[nodeConfig.NodeID] = cmd

		nodeIP := configFlags["public-ip"].(string)
		nodePort := uint(configFlags["http-port"].(float64))

		net.clients[nodeConfig.NodeID] = avalanchegoclient.NewClient(nodeIP, nodePort, nodePort, 20*time.Second)
		waitNode(net.clients[nodeConfig.NodeID])
	}

	return &net, nil
}

func waitNode(client *avalanchegoclient.Client) error {
	//info := client.InfoAPI()
	//bootstrapped, err := info.IsBootstrapped("P")
	//bootstrapped, err = info.IsBootstrapped("C")
	//bootstrapped, err = info.IsBootstrapped("X")
    return nil
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
