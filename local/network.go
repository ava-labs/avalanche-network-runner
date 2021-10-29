package local

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	defaultNodeNamePrefix = "node-"
	configFileName        = "config.json"
	stakingKeyFileName    = "staking.key"
	stakingCertFileName   = "staking.crt"
	genesisFileName       = "genesis.json"
	apiTimeout            = 5 * time.Second
)

// interface compliance
var _ network.Network = (*localNetwork)(nil)

// network keeps information uses for network management, and accessing all the nodes
type localNetwork struct {
	log logging.Logger
	// Node type --> Path to binary
	binMap map[NodeType]string
	// For node name generation
	nextID uint64
	// Node Name --> Node
	nodes map[string]*localNode
	// Keep insertion order
	nodeNames []string
}

// NewNetwork creates a network from given configuration and map of node kinds to binaries
func NewNetwork(log logging.Logger, networkConfig network.Config, binMap map[NodeType]string) (network.Network, error) {
	network := &localNetwork{
		nodes:  map[string]*localNode{},
		nextID: 1,
		binMap: binMap,
		log:    log,
	}
	for i, nodeConfig := range networkConfig.NodeConfigs {
		if _, err := network.AddNode(nodeConfig); err != nil {
			// release all resources for partially deployed network
			_ = network.Stop()
			return nil, fmt.Errorf("couldn't add node %d: %w", i, err)
		}
	}
	return network, nil
}

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
func (network *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	// If no name was given, use default name pattern
	if len(nodeConfig.Name) == 0 {
		nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, network.nextID)
		network.nextID++
	}
	// Enforce name uniqueness
	if _, ok := network.nodes[nodeConfig.Name]; ok {
		return nil, fmt.Errorf("repeated node name %s", nodeConfig.Name)
	}

	// [tmpDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Logs? Profiles?
	tmpDir := filepath.Join(os.TempDir(), "networkrunner", nodeConfig.Name)
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return nil, fmt.Errorf("couldn't create tmp dir %s", tmpDir)
	}

	// Flags for AvalancheGo that point to the files
	// we're about to create.
	flags := []string{}

	// Logs
	flags = append(flags, fmt.Sprintf("--%s=%s", config.LogsDirKey, filepath.Join(tmpDir, "logs")))
	// Write this node's config file if one is given
	configFilePath := filepath.Join(tmpDir, configFileName)
	if len(nodeConfig.ConfigFile) != 0 {
		if err := createFileAndWrite(configFilePath, nodeConfig.ConfigFile); err != nil {
			return nil, fmt.Errorf("error creating/writing config file: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath))
	}
	// Write this node's staking key file if one is given
	if len(nodeConfig.StakingKey) != 0 {
		stakingKeyFilePath := filepath.Join(tmpDir, stakingKeyFileName)
		if err := createFileAndWrite(stakingKeyFilePath, nodeConfig.StakingKey); err != nil {
			return nil, fmt.Errorf("error creating/writing staking key: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.StakingKeyPathKey, stakingKeyFilePath))
	}
	// Write this node's staking cert file if one is given
	if len(nodeConfig.StakingCert) != 0 {
		stakingCertFilePath := filepath.Join(tmpDir, stakingCertFileName)
		if err := createFileAndWrite(stakingCertFilePath, nodeConfig.StakingCert); err != nil {
			return nil, fmt.Errorf("error creating/writing staking cert: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.StakingCertPathKey, stakingCertFilePath))
	}
	// Write this node's genesis file if one is given
	if len(nodeConfig.GenesisFile) != 0 {
		genesisFilePath := filepath.Join(tmpDir, genesisFileName)
		if err := createFileAndWrite(genesisFilePath, nodeConfig.GenesisFile); err != nil {
			return nil, fmt.Errorf("error creating/writing genesis file: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.GenesisConfigFileKey, genesisFilePath))
	}
	// Write this node's C-Chain file if one is given
	if len(nodeConfig.CChainConfigFile) != 0 {
		cChainConfigFilePath := filepath.Join(tmpDir, "C", configFileName)
		if err := createFileAndWrite(cChainConfigFilePath, nodeConfig.CChainConfigFile); err != nil {
			return nil, fmt.Errorf("error creating/writing C-Chain config file: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ChainConfigDirKey, tmpDir))
	}
	// Get free http port
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("could not get free api port: %w", err)
	}
	httpPort := l.Addr().(*net.TCPAddr).Port
	l.Close()
	flags = append(flags, fmt.Sprintf("--%s=%d", config.HTTPPortKey, httpPort))
	network.log.Info(fmt.Sprintf("assigning api port %d to nohe %s\n", httpPort, nodeConfig.Name))
	// Path to AvalancheGo binary
	nodeType, ok := nodeConfig.Type.(NodeType)
	if !ok {
		return nil, fmt.Errorf("expected NodeType but got %T", nodeConfig.Type)
	}
	avalancheGoBinaryPath, ok := network.binMap[nodeType]
	if !ok {
		return nil, fmt.Errorf("got unexpected node type %v", nodeType)
	}
	// Start the AvalancheGo node and pass it the flags
	cmd := exec.Command(avalancheGoBinaryPath, flags...)
	if nodeConfig.LogsToStdout {
		ch := make(chan string, 1)
		read, w, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("could not get pipe for node stdout redirect: %w", err)
		}
		go func() {
			sc := bufio.NewScanner(read)
			for sc.Scan() {
				network.log.Info(fmt.Sprintf("[%s] %s", nodeConfig.Name, sc.Text()))
				fmt.Printf("[%s] %s\n", nodeConfig.Name, sc.Text())
			}
			close(ch)
		}()
		cmd.Stdout = w
		cmd.Stderr = w
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not execute cmd \"%s %s\": %w", avalancheGoBinaryPath, flags, err)
	}
	// Create a wrapper for this node so we can reference it later
	node := &localNode{
		name:   nodeConfig.Name,
		client: NewAPIClient("localhost", uint(httpPort), apiTimeout),
		cmd:    cmd,
		tmpDir: tmpDir,
	}
	network.nodeNames = append(network.nodeNames, node.name)
	network.nodes[node.name] = node
	return node, nil
}

// Ready closes readyCh is the network has initialized, or send error indicating network will not initialize
func (network *localNetwork) Ready() (chan struct{}, chan error) {
	readyCh := make(chan struct{})
	errorCh := make(chan error)
	go func() {
		for _, nodeName := range network.nodeNames {
			b := waitNode(network.nodes[nodeName].client)
			if !b {
				errorCh <- fmt.Errorf("timeout waiting for node %v", nodeName)
			}
			network.log.Info("node %q is up", nodeName)
		}
		close(readyCh)
	}()
	return readyCh, errorCh
}

func (network *localNetwork) GetNode(nodeName string) (node.Node, error) {
	node, ok := network.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node %q not found in network", nodeName)
	}
	return node, nil
}

func (network *localNetwork) GetNodesNames() []string {
	// TODO cache this
	names := make([]string, 0, len(network.nodes))
	for name := range network.nodes {
		names = append(names, name)
	}
	return names
}

func (network *localNetwork) Stop() error {
	for nodeName := range network.nodes {
		if err := network.RemoveNode(nodeName); err != nil {
			// TODO log error but continue
			return err
		}
	}
	return nil
}

func (network *localNetwork) RemoveNode(nodeName string) error {
	node, ok := network.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}
	delete(network.nodes, nodeName)
	var nodeNames []string
	for _, networkNodeName := range network.nodeNames {
		if networkNodeName != nodeName {
			nodeNames = append(nodeNames, networkNodeName)
		}
	}
	network.nodeNames = nodeNames
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if err := node.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("error sending SIGTERM to node %s: %w", nodeName, err)
	}
	return nil
}

// createFile creates a file with the given path and
// writes the given contents
func createFileAndWrite(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return nil
}

// waitNode waits until the node initializes, or timeouts
func waitNode(client api.Client) bool {
	info := client.InfoAPI()
	timeout := 1 * time.Minute
	pollTime := 10 * time.Second
	for t0 := time.Now(); time.Since(t0) <= timeout; time.Sleep(pollTime) {
		if bootstrapped, err := info.IsBootstrapped("P"); err != nil || !bootstrapped {
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("C"); err != nil || !bootstrapped {
			continue
		}
		if bootstrapped, err := info.IsBootstrapped("X"); err != nil || !bootstrapped {
			continue
		}
		return true
	}
	return false
}

func ParseNetworkConfigJSON(networkConfigJSON []byte) (*network.Config, error) {
	var networkConfigMap map[string]interface{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfigMap); err != nil {
		return nil, fmt.Errorf("couldn't unmarshall network config json: %s", err)
	}
	networkConfig := network.Config{}
	var networkGenesisFile []byte
	var networkCChainConfigFile []byte
	if networkConfigMap["GenesisFile"] != nil {
		networkGenesisFile = []byte(networkConfigMap["GenesisFile"].(string))
	}
	if networkConfigMap["CChainConfigFile"] != nil {
		networkCChainConfigFile = []byte(networkConfigMap["CChainConfigFile"].(string))
	}
	if networkConfigMap["NodeConfigs"] != nil {
		for _, nodeConfigMap := range networkConfigMap["NodeConfigs"].([]interface{}) {
			nodeConfigMap := nodeConfigMap.(map[string]interface{})
			nodeConfig := node.Config{}
			nodeConfig.GenesisFile = networkGenesisFile
			nodeConfig.CChainConfigFile = networkCChainConfigFile
			if nodeConfigMap["Type"] != nil {
				nodeConfig.Type = NodeType(nodeConfigMap["Type"].(float64))
			}
			if nodeConfigMap["Name"] != nil {
				nodeConfig.Name = nodeConfigMap["Name"].(string)
			}
			if nodeConfigMap["StakingKey"] != nil {
				nodeConfig.StakingKey = []byte(nodeConfigMap["StakingKey"].(string))
			}
			if nodeConfigMap["StakingCert"] != nil {
				nodeConfig.StakingCert = []byte(nodeConfigMap["StakingCert"].(string))
			}
			if nodeConfigMap["ConfigFile"] != nil {
				nodeConfig.ConfigFile = []byte(nodeConfigMap["ConfigFile"].(string))
			}
			if nodeConfigMap["CChainConfigFile"] != nil {
				nodeConfig.CChainConfigFile = []byte(nodeConfigMap["CChainConfigFile"].(string))
			}
			if nodeConfigMap["GenesisFile"] != nil {
				nodeConfig.GenesisFile = []byte(nodeConfigMap["GenesisFile"].(string))
			}
			if nodeConfigMap["LogsToStdout"] != nil {
				nodeConfig.LogsToStdout = nodeConfigMap["LogsToStdout"].(bool)
			}
			networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
		}
	}
	return &networkConfig, nil
}
