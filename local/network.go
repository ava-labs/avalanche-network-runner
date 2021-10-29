package local

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
	"golang.org/x/sync/errgroup"
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
	lock sync.RWMutex
	log  logging.Logger
	// True if the network has been stopped
	stopped bool // TODO use this
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
	if err := networkConfig.Validate(); err != nil {
		return nil, fmt.Errorf("config failed validation: %w", err)
	}
	log.Info("creating network with %d nodes", len(networkConfig.NodeConfigs))
	// Create the network
	net := &localNetwork{
		nodes:  map[string]*localNode{},
		nextID: 1,
		binMap: binMap,
		log:    log,
	}
	// Start all the nodes given in [networkConfig]
	for _, nodeConfig := range networkConfig.NodeConfigs {
		if _, err := net.AddNode(nodeConfig); err != nil {
			return nil, err
		}
	}
	// register signals to kill the network
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT)
	signal.Notify(signals, syscall.SIGTERM)

	// start up a new go routine to handle attempts to kill the application
	go func() {
		for range signals {
			err := net.Stop()
			if err != nil {
				log.Error("error while stopping network: %s", err)
			}
			close(signals)
		}
	}()
	return net, nil
}

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
func (ln *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	// If no name was given, use default name pattern
	if len(nodeConfig.Name) == 0 {
		nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, ln.nextID)
		ln.nextID++
	}

	// Enforce name uniqueness
	if _, ok := ln.nodes[nodeConfig.Name]; ok {
		return nil, fmt.Errorf("repeated node name %s", nodeConfig.Name)
	}

	// [tmpDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Logs? Profiles?
	tmpDir, err := os.MkdirTemp("", "avalanchego-network-runner-*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp dir: %w", err)
	}

	// Get free http port
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("could not get free api port: %w", err)
	}
	apiPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// Flags for AvalancheGo that point to the files
	// we're about to create.
	flags := []string{
		// Tell the node to put the database in [tmpDir]
		// TODO allow user to specify database path
		fmt.Sprintf("--%s=%s", config.DBPathKey, tmpDir),
		// Tell the node to put the log directory in [tmpDir]
		// TODO allow user to specify log dir path
		fmt.Sprintf("--%s=%s", config.LogsDirKey, filepath.Join(tmpDir, "logs")),
		// Tell the node to use the API port
		// TODO randomly generate this?
		fmt.Sprintf("--%s=%d", config.HTTPPortKey, apiPort),
	}

	ln.log.Info("adding node %q with files at %s and API port %d", nodeConfig.Name, tmpDir, apiPort) // TODO lower log level

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
	// Path to AvalancheGo binary
	nodeType, ok := nodeConfig.Type.(NodeType)
	if !ok {
		return nil, fmt.Errorf("expected NodeType but got %T", nodeConfig.Type)
	}
	avalancheGoBinaryPath, ok := ln.binMap[nodeType]
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
				ln.log.Info(fmt.Sprintf("[%s] %s", nodeConfig.Name, sc.Text()))
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
		client: NewAPIClient("localhost", uint(apiPort), apiTimeout),
		cmd:    cmd,
		tmpDir: tmpDir,
	}
	ln.nodeNames = append(ln.nodeNames, node.name)
	ln.nodes[node.name] = node
	return node, nil
}

// Returns a channel that is closed when the network
// is ready (all the initial nodes are up and healthy.)
// If an error is sent on this channel, the network
// is not healthy after the timeout.
func (net *localNetwork) Ready() chan error {
	net.lock.RLock()
	defer net.lock.RUnlock()

	readyCh := make(chan error, 1)
	nodes := make([]*localNode, 0, len(net.nodes))
	for _, node := range net.nodes {
		nodes = append(nodes, node)
	}
	go func() {
		// TODO use context for early return on shutdown or failure
		errGr := errgroup.Group{}
		for _, node := range nodes {
			node := node
			errGr.Go(func() error {
				healthy, _ := node.client.HealthAPI().AwaitHealthy(15, 10*time.Second)
				if healthy {
					net.log.Info("node %q became healthy", node.name)
					return nil
				}
				return fmt.Errorf("node %q not healthy after 1 minute", node.name)
			})
		}
		if err := errGr.Wait(); err != nil {
			readyCh <- err
		}
		close(readyCh)
	}()
	return readyCh
}

func (net *localNetwork) GetNode(nodeName string) (node.Node, error) {
	net.lock.RLock()
	defer net.lock.RUnlock()

	node, ok := net.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node %q not found in network", nodeName)
	}
	return node, nil
}

func (net *localNetwork) GetNodesNames() []string {
	net.lock.RLock()
	defer net.lock.RUnlock()

	// TODO cache this
	names := make([]string, 0, len(net.nodes))
	for name := range net.nodes {
		names = append(names, name)
	}
	return names
}

// TODO does this need to return an error?
func (net *localNetwork) Stop() error {
	net.lock.Lock()
	defer net.lock.Unlock()

	if net.stopped {
		net.log.Warn("stop() called but network was already stopped")
	}
	net.stopped = true
	net.log.Info("stopping network")
	for nodeName := range net.nodes {
		if err := net.removeNode(nodeName); err != nil {
			net.log.Warn("error removing node %q: %s", nodeName, err)
		}
	}
	net.log.Info("done stopping network") // todo remove
	return nil
}

// Sends a SIGTERM to the given node and removes it from this network
func (net *localNetwork) RemoveNode(nodeName string) error {
	net.lock.Lock()
	defer net.lock.Unlock()

	return net.removeNode(nodeName)
}

// Assumes [net.lock] is held
func (net *localNetwork) removeNode(nodeName string) error {
	net.log.Debug("removing node %q", nodeName)
	node, ok := net.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}
	delete(net.nodes, nodeName)
	var nodeNames []string
	for _, networkNodeName := range net.nodeNames {
		if networkNodeName != nodeName {
			nodeNames = append(nodeNames, networkNodeName)
		}
	}
	net.nodeNames = nodeNames
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if err := node.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("error sending SIGTERM to node %s: %w", nodeName, err)
	}
	if err := node.cmd.Wait(); err != nil {
		return fmt.Errorf("error waiting node %s to finish: %w", nodeName, err)
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

// TODO do we need this? It isn't used anywhere.
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
