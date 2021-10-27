package local

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	configFileName      = "config.json"
	stakingKeyFileName  = "staking.key"
	stakingCertFileName = "staking.crt"
	genesisFileName     = "genesis.json"
	apiTimeout          = 5 * time.Second
)

// interface compliance
var _ network.Network = (*localNetwork)(nil)

// network keeps information uses for network management, and accessing all the nodes
type localNetwork struct {
	log    logging.Logger        // logger used by the library
	binMap map[NodeType]string   // map node kind to string used in node set up (binary path)
	nextID uint64                // next id to be used in internal node id generation
	nodes  map[ids.ID]*localNode // access to nodes basic info and api
}

// NewNetwork creates a network from given configuration and map of node kinds to binaries
func NewNetwork(log logging.Logger, networkConfig network.Config, binMap map[NodeType]string) (network.Network, error) {
	if err := networkConfig.Validate(); err != nil {
		return nil, fmt.Errorf("config failed validation: %w", err)
	}
	net := &localNetwork{
		nodes:  map[ids.ID]*localNode{},
		nextID: 1,
		binMap: binMap,
		log:    log,
	}
	for _, nodeConfig := range networkConfig.NodeConfigs {
		if _, err := net.AddNode(nodeConfig); err != nil {
			return nil, err
		}
	}
	return net, nil
}

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
// func (net *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
// 	var configFlags map[string]interface{} = make(map[string]interface{})

// 	/* TODO what is this?
// 	if nodeConfig.BinKind == nil {
// 		return nil, fmt.Errorf("incomplete node config for node %v: BinKind field is empty", net.nextIntNodeID)
// 	}
// 	*/
// 	if nodeConfig.ConfigFlags == "" {
// 		return nil, fmt.Errorf("incomplete node config for node %v: ConfigFlags field is empty", net.nextID)
// 	}GenesisFile

// 	// tells if avalanchego is going to generate the cert/key for the node
// 	usesEphemeralCert, ok := configFlags[config.StakingEphemeralCertEnabledKey].(bool)
// 	if !ok {
// 		usesEphemeralCert = false
// 	}

// 	if !usesEphemeralCert {
// 		// cert/key is given in config
// 		if nodeConfig.StakingCert == nil {
// 			return nil, fmt.Errorf("incomplete node config for node %v: Cert field is empty", net.nextID)
// 		}
// 		certFname, ok := configFlags[config.StakingCertPathKey].(string)
// 		if !ok {
// 			return nil, fmt.Errorf("node %v lacks config flag %s", net.nextID, config.StakingCertPathKey)
// 		}
// 		if err := createFileAndWrite(certFname, []byte(nodeConfig.StakingCert)); err != nil {
// 			return nil, fmt.Errorf("couldn't create cert file %s for node %v: %w", certFname, net.nextID, err)
// 		}
// 		if nodeConfig.StakingKey == nil {
// 			return nil, fmt.Errorf("incomplete node config for node %v: PrivateKey field is empty", net.nextID)
// 		}
// 		keyFname, ok := configFlags[config.StakingKeyPathKey].(string)
// 		if !ok {
// 			return nil, fmt.Errorf("node %v lacks config flag %s", net.nextID, config.StakingKeyPathKey)
// 		}
// 		if err := createFileAndWrite(keyFname, []byte(nodeConfig.StakingKey)); err != nil {
// 			return nil, fmt.Errorf("couldn't create private key file %s for node %v: %w", keyFname, net.nextID, err)
// 		}
// 	}

// 	// initialize node avalanchego apis
// 	nodeIP, ok := configFlags[config.PublicIPKey].(string)
// 	if !ok {
// 		return nil, fmt.Errorf("node %v lacks config flag %s", net.nextID, config.PublicIPKey)
// 	}
// 	nodePortF, ok := configFlags[config.HTTPPortKey].(float64)
// 	if !ok {
// 		return nil, fmt.Errorf("node %v lacks config flag %s", net.nextID, config.HTTPPortKey)
// 	}
// 	nodePort := uint(nodePortF)
// 	apiClient := NewAPIClient(nodeIP, nodePort, 20*time.Second)

// 	// get binary from bin map and node kind, and execute it
// 	avalanchegoPath, ok := net.binMap[nodeConfig.Type.(NodeType)]
// 	if !ok {
// 		return nil, fmt.Errorf("could not found key %v in binMap for node %v", nodeConfig.Type.(NodeType), net.nextID)
// 	}
// 	configFileFlag := fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath)
// 	cmd := exec.Command(avalanchegoPath, configFileFlag)
// 	if err := cmd.Start(); err != nil {
// 		return nil, fmt.Errorf("could not execute cmd \"%s %s\" for node %v: %w", avalanchegoPath, configFileFlag, net.nextID, err)
// 	}

// 	// get internal node id from incremental uint
// 	// done in this way so the id when printed or converted, is correctly interpreted
// 	b := big.NewInt(0).SetUint64(net.nextID).Bytes()
// 	nodeID := ids.ID{}
// 	copy(nodeID[len(nodeID)-len(b):], b)
// 	net.nextID += 1

// 	node := localNode{id: nodeID, client: *apiClient}

// 	net.nodes[nodeID] = &node
// 	net.procs[nodeID] = cmd

// 	return &node, nil
// }

// AddNode prepares the files needed in filesystem by avalanchego, and executes it
func (net *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	// [tmpDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Logs? Profiles?
	tmpDir := os.TempDir()

	// Flags for AvalancheGo that point to the files
	// we're about to create.
	flags := []string{}
	configFilePath := filepath.Join(tmpDir, configFileName)

	// Write this node's config file if one is given
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
		cChainConfigFilePath := filepath.Join(tmpDir, "C", configFilePath)
		if err := createFileAndWrite(cChainConfigFilePath, nodeConfig.CChainConfigFile); err != nil {
			return nil, fmt.Errorf("error creating/writing C-Chain config file: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ChainConfigDirKey, tmpDir))
	}
	// Path to AvalancheGo binary
	avalancheGoBinaryPath, ok := net.binMap[nodeConfig.Type.(NodeType)]
	if !ok {
		return nil, fmt.Errorf("got unexpected node type %v", nodeConfig.Type.(NodeType))
	}
	// Start the AvalancheGo node and pass it the flags
	cmd := exec.Command(avalancheGoBinaryPath, flags...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not execute cmd \"%q %s\": %w", avalancheGoBinaryPath, flags, err)
	}
	// Create a wrapper for this node so we can reference it later
	node := &localNode{
		id:     ids.GenerateTestID(), // TODO don't use random ID
		client: NewAPIClient("localhost", nodeConfig.APIPort, apiTimeout),
		cmd:    cmd,
	}
	net.nodes[node.id] = node
	return node, nil
}

// Ready closes readyCh is the network has initialized, or send error indicating network will not initialize
func (net *localNetwork) Ready() (chan struct{}, chan error) {
	readyCh := make(chan struct{})
	errorCh := make(chan error)
	go func() {
		for id := range net.nodes {
			intID := big.NewInt(0).SetBytes(id[:])
			b := waitNode(net.nodes[id].client)
			if !b {
				errorCh <- fmt.Errorf("timeout waiting for node %v", intID)
			}
			net.log.Info("node %v is up", intID)
		}
		close(readyCh)
	}()
	return readyCh, errorCh
}

func (net *localNetwork) GetNode(nodeID ids.ID) (node.Node, error) {
	node, ok := net.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found in network", nodeID)
	}
	return node, nil
}

func (net *localNetwork) GetNodesIDs() []ids.ID {
	ks := make([]ids.ID, 0, len(net.nodes))
	for k := range net.nodes {
		ks = append(ks, k)
	}
	return ks
}

func (net *localNetwork) Stop() error {
	for nodeID := range net.nodes {
		if err := net.RemoveNode(nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (net *localNetwork) RemoveNode(nodeID ids.ID) error {
	node, ok := net.nodes[nodeID]
	if !ok {
		return fmt.Errorf("node %s not found in network", nodeID)
	}
	delete(net.nodes, nodeID)
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if err := node.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("error sending SIGTERM to %s: %w", node.nodeID, err)
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
