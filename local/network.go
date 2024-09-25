package local

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/beacon"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

const (
	defaultNodeNamePrefix     = "node"
	configFileName            = "config.json"
	upgradeConfigFileName     = "upgrade.json"
	stakingKeyFileName        = "staker.key"
	stakingCertFileName       = "staker.crt"
	stakingSigningKeyFileName = "signer.key"
	genesisFileName           = "genesis.json"
	upgradeFileName           = "upgrade.json"
	stopTimeout               = 30 * time.Second
	healthCheckFreq           = 3 * time.Second
	snapshotPrefix            = "anr-snapshot-"
	networkRootDirPrefix      = "network"
	defaultDBSubdir           = "db"
	defaultLogsSubdir         = "logs"
)

// interface compliance
var (
	_ network.Network    = (*localNetwork)(nil)
	_ NodeProcessCreator = (*nodeProcessCreator)(nil)

	warnFlags = map[string]struct{}{
		config.NetworkNameKey:  {},
		config.BootstrapIPsKey: {},
		config.BootstrapIDsKey: {},
	}
	chainConfigSubDir  = "chainConfigs"
	subnetConfigSubDir = "subnetConfigs"

	snapshotsRelPath = filepath.Join(".avalanche-network-runner", "snapshots")

	ErrSnapshotNotFound = errors.New("snapshot not found")
)

// network keeps information uses for network management, and accessing all the nodes
type localNetwork struct {
	lock sync.RWMutex
	log  logging.Logger
	// This network's ID.
	networkID uint32
	// This network's genesis file.
	// Must not be nil.
	genesis []byte
	// This network's upgrade file.
	// May be nil
	upgrade []byte
	// Used to create a new API client
	newAPIClientF api.NewAPIClientF
	// Used to create new node processes
	nodeProcessCreator NodeProcessCreator
	stopOnce           sync.Once
	// Closed when Stop begins.
	onStopCh chan struct{}
	// For node name generation
	nextNodeSuffix uint64
	// Node Name --> Node
	nodes map[string]*localNode
	// Set of nodes that new nodes will bootstrap from.
	bootstraps beacon.Set
	// rootDir is the root directory under which we write all node
	// databases, etc
	rootDir string
	// logRootDir is the root directory under which we write all node logs
	logRootDir string
	// directory where networks can be persistently saved
	snapshotsDir string
	// flags to apply to all nodes per default
	flags map[string]interface{}
	// binary path to use per default
	binaryPath string
	// custom genesis path if defined
	genesisPath string
	// chain config files to use per default
	chainConfigFiles map[string]string
	// upgrade config files to use per default
	upgradeConfigFiles map[string]string
	// subnet config files to use per default
	subnetConfigFiles map[string]string
	// if true, for ports given in conf that are already taken, assign new random ones
	reassignPortsIfUsed bool
	// if true, direct this node's Stdout to os.Stdout
	redirectStdout bool
	// if true, direct this node's Stderr to os.Stderr
	redirectStderr bool
	// map from subnet id to elastic subnet tx id
	subnetID2ElasticSubnetID map[ids.ID]ids.ID
	// map from blockchain id to blockchain aliases
	blockchainAliases map[string][]string
	// wallet private key used. IF nil, genesis ewoq key will be used
	walletPrivateKey string
}

type deprecatedFlagEsp struct {
	Version  string `json:"version"`
	OldName  string `json:"old_name"`
	NewName  string `json:"new_name"`
	ValueMap string `json:"value_map"`
}

var (
	//go:embed default
	embeddedDefaultNetworkConfigDir embed.FS
	//go:embed deprecatedFlagsSupport.json
	deprecatedFlagsSupportBytes []byte
	deprecatedFlagsSupport      []deprecatedFlagEsp
	// snapshots directory
	DefaultSnapshotsDir string
)

// populate default network config from embedded default directory
func init() {
	// load deprecated avago flags support information
	if err := json.Unmarshal(deprecatedFlagsSupportBytes, &deprecatedFlagsSupport); err != nil {
		panic(err)
	}
	// create default snapshots dir
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	DefaultSnapshotsDir = filepath.Join(usr.HomeDir, snapshotsRelPath)
}

// NewNetwork returns a new network that uses the given log.
// Files (e.g. logs, databases) default to being written at directory [rootDir].
// If there isn't a directory at [dir] one will be created.
// If len([dir]) == 0, files will be written underneath a new temporary directory.
// Snapshots are saved to snapshotsDir, defaults to DefaultSnapshotsDir if not given
func NewNetwork(
	log logging.Logger,
	networkConfig network.Config,
	rootDir string,
	logRootDir string,
	snapshotsDir string,
	reassignPortsIfUsed bool,
	redirectStdout bool,
	redirectStderr bool,
	walletPrivateKey string,
	genesisPath string,
) (network.Network, error) {
	net, err := newNetwork(
		log,
		api.NewAPIClient,
		&nodeProcessCreator{
			colorPicker: utils.NewColorPicker(),
			log:         log,
			stdout:      os.Stdout,
			stderr:      os.Stderr,
		},
		rootDir,
		logRootDir,
		snapshotsDir,
		reassignPortsIfUsed,
		redirectStdout,
		redirectStderr,
		walletPrivateKey,
		genesisPath,
		utils.BeaconMapToSet(networkConfig.BeaconConfig),
	)
	if err != nil {
		return net, err
	}
	return net, net.loadConfig(context.Background(), networkConfig)
}

// See NewNetwork.
// [newAPIClientF] is used to create new API clients.
// [nodeProcessCreator] is used to launch new avalanchego processes.
func newNetwork(
	log logging.Logger,
	newAPIClientF api.NewAPIClientF,
	nodeProcessCreator NodeProcessCreator,
	rootDir string,
	logRootDir string,
	snapshotsDir string,
	reassignPortsIfUsed bool,
	redirectStdout bool,
	redirectStderr bool,
	walletPrivateKey string,
	genesisPath string,
	beaconSet beacon.Set,
) (*localNetwork, error) {
	var err error
	if rootDir == "" {
		anrRootDir := filepath.Join(os.TempDir(), constants.RootDirPrefix)
		err = os.MkdirAll(anrRootDir, os.ModePerm)
		if err != nil {
			return nil, err
		}
		networkRootDir := filepath.Join(anrRootDir, networkRootDirPrefix)
		rootDir, err = utils.MkDirWithTimestamp(networkRootDir)
		if err != nil {
			return nil, err
		}
	}
	if logRootDir == "" {
		logRootDir = rootDir
	}
	if snapshotsDir == "" {
		snapshotsDir = DefaultSnapshotsDir
	}
	// create the snapshots dir if not present
	err = os.MkdirAll(snapshotsDir, os.ModePerm)
	if err != nil {
		return nil, err
	}
	// Create the network
	net := &localNetwork{
		nextNodeSuffix:           1,
		nodes:                    map[string]*localNode{},
		onStopCh:                 make(chan struct{}),
		log:                      log,
		bootstraps:               beaconSet,
		newAPIClientF:            newAPIClientF,
		nodeProcessCreator:       nodeProcessCreator,
		rootDir:                  rootDir,
		logRootDir:               logRootDir,
		snapshotsDir:             snapshotsDir,
		reassignPortsIfUsed:      reassignPortsIfUsed,
		redirectStdout:           redirectStdout,
		redirectStderr:           redirectStderr,
		subnetID2ElasticSubnetID: map[ids.ID]ids.ID{},
		blockchainAliases:        map[string][]string{},
		walletPrivateKey:         walletPrivateKey,
		genesisPath:              genesisPath,
	}
	return net, nil
}

// NewDefaultNetwork returns a new network using a pre-defined
// network configuration.
// The following addresses are pre-funded:
// X-Chain Address 1:     X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// X-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// P-Chain Address 1:     P-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// P-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// C-Chain Address:       0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC
// C-Chain Address Key:   56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027
// The following nodes are validators:
// * NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg
// * NodeID-MFrZFVCXPv5iCn6M9K6XduxGTYp891xXZ
// * NodeID-NFBbbJ4qCmNaCzeW7sxErhvWqvEQMnYcN
// * NodeID-GWPcbFJZFfZreETSoWjPimr846mXEKCtu
// * NodeID-P7oB2McjBGgW2NXXWVYjV8JEDFoW9xDE5
func NewDefaultNetwork(
	log logging.Logger,
	binaryPath string,
	reassignPortsIfUsed bool,
	redirectStdout bool,
	redirectStderr bool,
) (network.Network, error) {
	config, err := NewDefaultConfig(binaryPath, constants.DefaultNetworkID, "", "")
	if err != nil {
		return nil, err
	}
	return NewNetwork(
		log,
		config,
		"",
		"",
		"",
		reassignPortsIfUsed,
		redirectStdout,
		redirectStderr,
		"",
		"",
	)
}

func loadDefaultNetworkFiles() (map[string]interface{}, []byte, []*utils.NodeKeys, error) {
	configsDir, err := fs.Sub(embeddedDefaultNetworkConfigDir, "default")
	if err != nil {
		return nil, nil, nil, err
	}
	// network flags
	flagsBytes, err := fs.ReadFile(configsDir, "flags.json")
	if err != nil {
		return nil, nil, nil, err
	}
	flags := map[string]interface{}{}
	if err = json.Unmarshal(flagsBytes, &flags); err != nil {
		return nil, nil, nil, err
	}
	// c-chain config
	cChainConfig, err := fs.ReadFile(configsDir, "cchain_config.json")
	if err != nil {
		return nil, nil, nil, err
	}
	nodeKeys := []*utils.NodeKeys{}
	for i := 0; i < constants.DefaultNumNodes; i++ {
		nodeDir := fmt.Sprintf("node%d", i+1)
		stakingKey, err := fs.ReadFile(configsDir, filepath.Join(nodeDir, stakingKeyFileName))
		if err != nil {
			return nil, nil, nil, err
		}
		stakingCert, err := fs.ReadFile(configsDir, filepath.Join(nodeDir, stakingCertFileName))
		if err != nil {
			return nil, nil, nil, err
		}
		blsKey, err := fs.ReadFile(configsDir, filepath.Join(nodeDir, stakingSigningKeyFileName))
		if err != nil {
			return nil, nil, nil, err
		}
		nodeKeys = append(nodeKeys, &utils.NodeKeys{
			StakingKey:  stakingKey,
			StakingCert: stakingCert,
			BlsKey:      blsKey,
		})
	}
	return flags, cChainConfig, nodeKeys, nil
}

// NewDefaultConfigNNodes creates a new default network config, with an arbitrary number of nodes
func NewDefaultConfigNNodes(
	binaryPath string,
	numNodes uint32,
	networkID uint32,
	genesisPath string,
	upgradePath string,
) (network.Config, error) {
	if networkID == 0 {
		networkID = constants.DefaultNetworkID
	}
	flags, cChainConfig, nodeKeys, err := loadDefaultNetworkFiles()
	if err != nil {
		return network.Config{}, err
	}
	if int(numNodes) > constants.DefaultNumNodes {
		toAdd := int(numNodes) - constants.DefaultNumNodes
		newNodeKeys, err := utils.GenerateKeysForNodes(toAdd)
		if err != nil {
			return network.Config{}, err
		}
		nodeKeys = append(nodeKeys, newNodeKeys...)
	}
	if int(numNodes) < constants.DefaultNumNodes {
		nodeKeys = nodeKeys[:numNodes]
	}
	nodeConfigs := []node.Config{}
	port := constants.FirstAPIPort
	for _, keys := range nodeKeys {
		encodedKeys := utils.EncodeNodeKeys(keys)
		nodeConfig := node.Config{
			StakingKey:        encodedKeys.StakingKey,
			StakingCert:       encodedKeys.StakingCert,
			StakingSigningKey: encodedKeys.BlsKey,
			Flags: map[string]interface{}{
				config.HTTPPortKey:    port,
				config.StakingPortKey: port + 1,
			},
		}
		if !utils.IsPublicNetwork(networkID) {
			nodeConfig.IsBeacon = true
		}
		nodeConfigs = append(nodeConfigs, nodeConfig)
		port += 2
	}
	if int(numNodes) == 1 && !utils.IsPublicNetwork(networkID) {
		flags[config.SybilProtectionEnabledKey] = false
	}
	cfg := network.Config{
		NetworkID:          networkID,
		Flags:              flags,
		NodeConfigs:        nodeConfigs,
		BinaryPath:         binaryPath,
		ChainConfigFiles:   map[string]string{},
		UpgradeConfigFiles: map[string]string{},
		SubnetConfigFiles:  map[string]string{},
	}
	if len(upgradePath) != 0 {
		upgrade, err := os.ReadFile(upgradePath)
		if err != nil {
			return network.Config{}, fmt.Errorf("could not read upgrade file: %w", err)
		}
		cfg.Upgrade = string(upgrade)
	}
	if utils.IsCustomNetwork(networkID) {
		var genesis []byte
		if len(genesisPath) != 0 {
			if _, err := os.Stat(genesisPath); err != nil {
				return network.Config{}, fmt.Errorf("could not find genesis file: %w", err)
			}
			genesis, err = os.ReadFile(genesisPath)
			if err != nil {
				return network.Config{}, fmt.Errorf("could not read genesis file: %w", err)
			}
		} else {
			genesis, err = utils.GenerateGenesis(networkID, nodeKeys)
			if err != nil {
				return network.Config{}, err
			}
		}
		cfg.Genesis = string(genesis)
		cfg.ChainConfigFiles = map[string]string{
			"C": string(cChainConfig),
		}
	}
	return cfg, nil
}

// NewDefaultConfig creates a new default network config
func NewDefaultConfig(
	binaryPath string,
	networkID uint32,
	genesisPath string,
	upgradePath string,
) (network.Config, error) {
	return NewDefaultConfigNNodes(
		binaryPath,
		constants.DefaultNumNodes,
		networkID,
		genesisPath,
		upgradePath,
	)
}

func (ln *localNetwork) loadConfig(ctx context.Context, networkConfig network.Config) error {
	if err := networkConfig.Validate(); err != nil {
		return fmt.Errorf("config failed validation: %w", err)
	}
	ln.log.Info("creating network", zap.Int("node-num", len(networkConfig.NodeConfigs)))

	ln.networkID = networkConfig.NetworkID
	if len(networkConfig.Genesis) != 0 {
		ln.genesis = []byte(networkConfig.Genesis)
		genesisNetworkID, err := utils.NetworkIDFromGenesis(ln.genesis)
		if err != nil {
			return err
		}
		switch {
		case ln.networkID == 0:
			ln.networkID = genesisNetworkID
		case ln.networkID != genesisNetworkID:
			if ln.genesis, err = utils.SetGenesisNetworkID(ln.genesis, ln.networkID); err != nil {
				return fmt.Errorf("couldn't set network ID to genesis: %w", err)
			}
		}
	}

	// save node defaults
	ln.flags = networkConfig.Flags
	ln.binaryPath = networkConfig.BinaryPath
	ln.chainConfigFiles = networkConfig.ChainConfigFiles
	if ln.chainConfigFiles == nil {
		ln.chainConfigFiles = map[string]string{}
	}
	ln.upgradeConfigFiles = networkConfig.UpgradeConfigFiles
	if ln.upgradeConfigFiles == nil {
		ln.upgradeConfigFiles = map[string]string{}
	}
	ln.subnetConfigFiles = networkConfig.SubnetConfigFiles
	if ln.subnetConfigFiles == nil {
		ln.subnetConfigFiles = map[string]string{}
	}

	// Sort node configs so beacons start first
	var nodeConfigs []node.Config
	for _, nodeConfig := range networkConfig.NodeConfigs {
		if nodeConfig.IsBeacon {
			nodeConfigs = append(nodeConfigs, nodeConfig)
		}
	}
	for _, nodeConfig := range networkConfig.NodeConfigs {
		if !nodeConfig.IsBeacon {
			nodeConfigs = append(nodeConfigs, nodeConfig)
		}
	}

	for _, nodeConfig := range nodeConfigs {
		if _, err := ln.addNode(nodeConfig); err != nil {
			if err := ln.stop(ctx); err != nil {
				// Clean up nodes already created
				ln.log.Debug("error stopping network", zap.Error(err))
			}
			return fmt.Errorf("error adding node %s: %w", nodeConfig.Name, err)
		}
	}

	ln.upgrade = []byte(networkConfig.Upgrade)

	return nil
}

// See network.Network
func (ln *localNetwork) GetNetworkID() (uint32, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if ln.stopCalled() {
		return 0, network.ErrStopped
	}

	return ln.networkID, nil
}

// See network.Network
func (ln *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if ln.stopCalled() {
		return nil, network.ErrStopped
	}

	node, err := ln.addNode(nodeConfig)
	if err != nil {
		return node, err
	}
	return node, ln.persistNetwork()
}

// Assumes [ln.lock] is held and [ln.Stop] hasn't been called.
func (ln *localNetwork) addNode(nodeConfig node.Config) (node.Node, error) {
	if nodeConfig.Flags == nil {
		nodeConfig.Flags = map[string]interface{}{}
	}
	if nodeConfig.ChainConfigFiles == nil {
		nodeConfig.ChainConfigFiles = map[string]string{}
	}
	if nodeConfig.UpgradeConfigFiles == nil {
		nodeConfig.UpgradeConfigFiles = map[string]string{}
	}
	if nodeConfig.SubnetConfigFiles == nil {
		nodeConfig.SubnetConfigFiles = map[string]string{}
	}

	// load node defaults
	if nodeConfig.BinaryPath == "" {
		nodeConfig.BinaryPath = ln.binaryPath
	}
	for k, v := range ln.chainConfigFiles {
		_, ok := nodeConfig.ChainConfigFiles[k]
		if !ok {
			nodeConfig.ChainConfigFiles[k] = v
		}
	}
	for k, v := range ln.upgradeConfigFiles {
		_, ok := nodeConfig.UpgradeConfigFiles[k]
		if !ok {
			nodeConfig.UpgradeConfigFiles[k] = v
		}
	}
	for k, v := range ln.subnetConfigFiles {
		_, ok := nodeConfig.SubnetConfigFiles[k]
		if !ok {
			nodeConfig.SubnetConfigFiles[k] = v
		}
	}
	addNetworkFlags(ln.flags, nodeConfig.Flags)

	// it shouldn't happen that just one is empty, most probably both,
	// but in any case if just one is empty it's unusable so we just assign a new one.
	if nodeConfig.StakingCert == "" || nodeConfig.StakingKey == "" {
		stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
		if err != nil {
			return nil, fmt.Errorf("couldn't generate staking Cert/Key: %w", err)
		}
		nodeConfig.StakingCert = string(stakingCert)
		nodeConfig.StakingKey = string(stakingKey)
	}
	if nodeConfig.StakingSigningKey == "" {
		key, err := bls.NewSecretKey()
		if err != nil {
			return nil, fmt.Errorf("couldn't generate new signing key: %w", err)
		}
		keyBytes := bls.SecretKeyToBytes(key)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)
		nodeConfig.StakingSigningKey = encodedKey
	}

	if err := ln.setNodeName(&nodeConfig); err != nil {
		return nil, err
	}

	isPausedNode := ln.isPausedNode(&nodeConfig)

	nodeDir, err := setNodeDir(ln.log, ln.rootDir, nodeConfig.Name)
	if err != nil {
		return nil, err
	}
	nodeLogDir := ""
	if ln.rootDir != ln.logRootDir {
		nodeLogDir, err = setNodeDir(ln.log, ln.logRootDir, nodeConfig.Name)
		if err != nil {
			return nil, err
		}
	}

	// If config file is given, don't overwrite API port, P2P port, DB path, logs path
	var configFile map[string]interface{}
	if len(nodeConfig.ConfigFile) != 0 {
		if err := json.Unmarshal([]byte(nodeConfig.ConfigFile), &configFile); err != nil {
			return nil, fmt.Errorf("couldn't unmarshal config file: %w", err)
		}
	}

	// Get node version
	nodeSemVer, err := ln.getNodeSemVer(nodeConfig)
	if err != nil {
		return nil, err
	}

	nodeData, err := ln.buildArgs(nodeSemVer, configFile, nodeDir, nodeLogDir, &nodeConfig)
	if err != nil {
		return nil, err
	}

	// Parse this node's ID
	nodeID, err := utils.ToNodeID([]byte(nodeConfig.StakingKey), []byte(nodeConfig.StakingCert))
	if err != nil {
		return nil, fmt.Errorf("couldn't get node ID: %w", err)
	}

	// If this node is a beacon, add its IP/ID to the beacon lists.
	// Note that we do this *after* we set this node's bootstrap IPs/IDs
	// so this node won't try to use itself as a beacon.
	ip, err := netip.ParseAddr(nodeData.publicIP)
	if err != nil {
		return nil, err
	}
	if !isPausedNode && nodeConfig.IsBeacon {
		if err := ln.bootstraps.Add(beacon.New(nodeID, netip.AddrPortFrom(
			ip,
			nodeData.p2pPort,
		))); err != nil {
			return nil, err
		}
	}

	// Start the AvalancheGo node and pass it the flags defined above
	nodeProcess, err := ln.nodeProcessCreator.NewNodeProcess(nodeConfig, nodeData.args...)
	if err != nil {
		return nil, fmt.Errorf(
			"couldn't create new node process with binary %q and args %v: %w",
			nodeConfig.BinaryPath, nodeData.args, err,
		)
	}

	ln.log.Info(
		"adding node",
		zap.String("node-name", nodeConfig.Name),
		zap.String("node-dir", nodeData.dataDir),
		zap.String("log-dir", nodeData.logsDir),
		zap.String("db-dir", nodeData.dbDir),
		zap.Uint16("p2p-port", nodeData.p2pPort),
		zap.Uint16("api-port", nodeData.apiPort),
	)

	ln.log.Debug(
		"starting node",
		zap.String("name", nodeConfig.Name),
		zap.String("binaryPath", nodeConfig.BinaryPath),
		zap.Strings("args", nodeData.args),
	)

	// Create a wrapper for this node so we can reference it later
	node := &localNode{
		name:          nodeConfig.Name,
		nodeID:        nodeID,
		networkID:     ln.networkID,
		client:        ln.newAPIClientF(nodeData.publicIP, nodeData.apiPort),
		process:       nodeProcess,
		apiPort:       nodeData.apiPort,
		p2pPort:       nodeData.p2pPort,
		getConnFunc:   defaultGetConnFunc,
		dataDir:       nodeData.dataDir,
		dbDir:         nodeData.dbDir,
		logsDir:       nodeData.logsDir,
		config:        nodeConfig,
		pluginDir:     nodeData.pluginDir,
		httpHost:      nodeData.httpHost,
		attachedPeers: map[string]peer.Peer{},
	}
	ln.nodes[node.name] = node
	return node, ln.persistNetwork()
}

// See network.Network
func (ln *localNetwork) Healthy(ctx context.Context) error {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	return ln.healthy(ctx)
}

func (ln *localNetwork) healthy(ctx context.Context) error {
	ln.log.Info("checking local network healthiness", zap.Int("num-of-nodes", len(ln.nodes)))

	// Return unhealthy if the network is stopped
	if ln.stopCalled() {
		return network.ErrStopped
	}

	// Derive a new context that's cancelled when Stop is called,
	// so that calls to Healthy() below immediately return.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func(ctx context.Context) {
		// This goroutine runs until [ln.Stop] is called
		// or this function returns.
		select {
		case <-ln.onStopCh:
			cancel()
		case <-ctx.Done():
		}
	}(ctx)

	errGr, ctx := errgroup.WithContext(ctx)
	for _, node := range ln.nodes {
		if node.paused {
			// no health check for paused nodes
			continue
		}
		node := node
		nodeName := node.GetName()
		errGr.Go(func() error {
			// Every [healthCheckFreq], query node for health status.
			// Do this until ctx timeout or network closed.
			for {
				if node.Status() != status.Running {
					// If we had stopped this node ourselves, it wouldn't be in [ln.nodes].
					// Since it is, it means the node stopped unexpectedly.
					return fmt.Errorf("node %q stopped unexpectedly", nodeName)
				}
				health, err := node.client.HealthAPI().Health(ctx, nil)
				if err == nil && health.Healthy {
					ln.log.Debug("node became healthy", zap.String("name", nodeName))
					return nil
				}
				select {
				case <-ctx.Done():
					return fmt.Errorf("node %q failed to become healthy within timeout, or network stopped", nodeName)
				case <-time.After(healthCheckFreq):
				}
			}
		})
	}
	// Wait until all nodes are ready or timeout
	return errGr.Wait()
}

// See network.Network
func (ln *localNetwork) GetNode(nodeName string) (node.Node, error) {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	if ln.stopCalled() {
		return nil, network.ErrStopped
	}

	node, ok := ln.nodes[nodeName]
	if !ok {
		return nil, network.ErrNodeNotFound
	}
	return node, nil
}

// See network.Network
func (ln *localNetwork) GetNodeNames() ([]string, error) {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	if ln.stopCalled() {
		return nil, network.ErrStopped
	}

	return maps.Keys(ln.nodes), nil
}

// See network.Network
func (ln *localNetwork) GetAllNodes() (map[string]node.Node, error) {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	if ln.stopCalled() {
		return nil, network.ErrStopped
	}

	nodesCopy := make(map[string]node.Node, len(ln.nodes))
	for name, node := range ln.nodes {
		nodesCopy[name] = node
	}
	return nodesCopy, nil
}

func (ln *localNetwork) Stop(ctx context.Context) error {
	err := network.ErrStopped
	ln.stopOnce.Do(
		func() {
			close(ln.onStopCh)

			ln.lock.Lock()
			defer ln.lock.Unlock()

			err = ln.stop(ctx)
		},
	)
	return err
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) stop(ctx context.Context) error {
	errs := wrappers.Errs{}
	for nodeName := range ln.nodes {
		stopCtx, stopCtxCancel := context.WithTimeout(ctx, stopTimeout)
		if err := ln.removeNode(stopCtx, nodeName); err != nil {
			ln.log.Error("error stopping node", zap.String("name", nodeName), zap.Error(err))
			errs.Add(err)
		}
		stopCtxCancel()
	}
	ln.log.Info("done stopping network")
	return errs.Err
}

// Sends a SIGTERM to the given node and removes it from this network.
func (ln *localNetwork) RemoveNode(ctx context.Context, nodeName string) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if ln.stopCalled() {
		return network.ErrStopped
	}
	if err := ln.removeNode(ctx, nodeName); err != nil {
		return err
	}
	return ln.persistNetwork()
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) removeNode(ctx context.Context, nodeName string) error {
	ln.log.Debug("removing node", zap.String("name", nodeName))
	node, ok := ln.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}

	paused := node.paused

	// If the node wasn't a beacon, we don't care
	_ = ln.bootstraps.RemoveByID(node.nodeID)
	delete(ln.nodes, nodeName)

	if !paused {
		// cchain eth api uses a websocket connection and must be closed before stopping the node,
		// to avoid errors logs at client
		node.client.CChainEthAPI().Close()
		if exitCode := node.process.Stop(ctx); exitCode != 0 {
			return fmt.Errorf("node %q exited with exit code: %d", nodeName, exitCode)
		}
	}
	return nil
}

// Sends a SIGTERM to the given node and keeps it in the network with paused state
func (ln *localNetwork) PauseNode(ctx context.Context, nodeName string) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	if ln.stopCalled() {
		return network.ErrStopped
	}
	if err := ln.pauseNode(ctx, nodeName); err != nil {
		return err
	}
	return ln.persistNetwork()
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) pauseNode(ctx context.Context, nodeName string) error {
	ln.log.Debug("pausing node", zap.String("name", nodeName))
	node, ok := ln.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}
	if node.paused {
		return fmt.Errorf("node has been paused already")
	}
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if exitCode := node.process.Stop(ctx); exitCode != 0 {
		return fmt.Errorf("node %q exited with exit code: %d", nodeName, exitCode)
	}
	node.paused = true
	return nil
}

// Resume previously paused [nodeName] using the same config.
func (ln *localNetwork) ResumeNode(
	ctx context.Context,
	nodeName string,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if err := ln.resumeNode(ctx, nodeName); err != nil {
		return err
	}
	return ln.persistNetwork()
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) resumeNode(
	_ context.Context,
	nodeName string,
) error {
	node, ok := ln.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}
	if !node.paused {
		return fmt.Errorf("node has not been paused")
	}
	nodeConfig := node.GetConfig()
	nodeConfig.Flags[config.DataDirKey] = node.GetDataDir()
	nodeConfig.Flags[config.DBPathKey] = node.GetDbDir()
	nodeConfig.Flags[config.LogsDirKey] = node.GetLogsDir()
	nodeConfig.Flags[config.HTTPPortKey] = int(node.GetAPIPort())
	nodeConfig.Flags[config.StakingPortKey] = int(node.GetP2PPort())
	if _, err := ln.addNode(nodeConfig); err != nil {
		return err
	}
	return nil
}

// Restart [nodeName] using the same config, optionally changing [binaryPath],
// [pluginDir], [trackSubnets], [chainConfigs], [upgradeConfigs], [subnetConfigs]
func (ln *localNetwork) RestartNode(
	ctx context.Context,
	nodeName string,
	binaryPath string,
	pluginDir string,
	trackSubnets string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if err := ln.restartNode(
		ctx,
		nodeName,
		binaryPath,
		pluginDir,
		trackSubnets,
		chainConfigs,
		upgradeConfigs,
		subnetConfigs,
	); err != nil {
		return err
	}
	return ln.persistNetwork()
}

func (ln *localNetwork) restartNode(
	ctx context.Context,
	nodeName string,
	binaryPath string,
	pluginDir string,
	trackSubnets string,
	chainConfigs map[string]string,
	upgradeConfigs map[string]string,
	subnetConfigs map[string]string,
) error {
	node, ok := ln.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}

	nodeConfig := node.GetConfig()

	if binaryPath != "" {
		nodeConfig.BinaryPath = binaryPath
	}
	if pluginDir != "" {
		nodeConfig.Flags[config.PluginDirKey] = pluginDir
	}

	if trackSubnets != "" {
		nodeConfig.Flags[config.TrackSubnetsKey] = trackSubnets
	}

	// keep same ports, dbdir in node flags
	nodeConfig.Flags[config.DataDirKey] = node.GetDataDir()
	nodeConfig.Flags[config.DBPathKey] = node.GetDbDir()
	nodeConfig.Flags[config.LogsDirKey] = node.GetLogsDir()
	nodeConfig.Flags[config.HTTPPortKey] = int(node.GetAPIPort())
	nodeConfig.Flags[config.StakingPortKey] = int(node.GetP2PPort())
	// apply chain configs
	for k, v := range chainConfigs {
		nodeConfig.ChainConfigFiles[k] = v
	}
	// apply upgrade configs
	for k, v := range upgradeConfigs {
		nodeConfig.UpgradeConfigFiles[k] = v
	}
	// apply subnet configs
	for k, v := range subnetConfigs {
		nodeConfig.SubnetConfigFiles[k] = v
	}

	if !node.paused {
		if err := ln.removeNode(ctx, nodeName); err != nil {
			return err
		}
	}

	if _, err := ln.addNode(nodeConfig); err != nil {
		return err
	}

	return nil
}

// Returns whether Stop has been called.
func (ln *localNetwork) stopCalled() bool {
	select {
	case <-ln.onStopCh:
		return true
	default:
		return false
	}
}

func (ln *localNetwork) isPausedNode(nodeConfig *node.Config) bool {
	if node, ok := ln.nodes[nodeConfig.Name]; ok && node.paused {
		return true
	}
	return false
}

// Set [nodeConfig].Name if it isn't given and assert it's unique.
func (ln *localNetwork) setNodeName(nodeConfig *node.Config) error {
	// If no name was given, use default name pattern
	if len(nodeConfig.Name) == 0 {
		for {
			nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, ln.nextNodeSuffix)
			_, ok := ln.nodes[nodeConfig.Name]
			if !ok {
				break
			}
			ln.nextNodeSuffix++
		}
	}
	// Enforce name uniqueness
	// Only paused nodes are enabled to be started with repeated name
	if node, ok := ln.nodes[nodeConfig.Name]; ok && !node.paused {
		return fmt.Errorf("repeated node name %q", nodeConfig.Name)
	}
	return nil
}

type buildArgsReturn struct {
	args      []string
	publicIP  string
	apiPort   uint16
	p2pPort   uint16
	dataDir   string
	dbDir     string
	logsDir   string
	pluginDir string
	httpHost  string
}

// buildArgs returns the:
// 1) Args for avago execution
// 2) API port
// 3) P2P port
// of the node being added with config [nodeConfig], config file [configFile],
// and directory at [nodeDir].
// [nodeConfig.Flags] must not be nil
func (ln *localNetwork) buildArgs(
	nodeSemVer string,
	configFile map[string]interface{},
	nodeDir string,
	nodeLogDir string,
	nodeConfig *node.Config,
) (buildArgsReturn, error) {
	// httpHost from all configs for node
	httpHost, err := getConfigEntry(nodeConfig.Flags, configFile, config.HTTPHostKey, "")
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Tell the node to put all node related data in [nodeDir] unless given in config file
	dataDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.DataDirKey, nodeDir)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// pluginDir from all configs for node
	pluginDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.PluginDirKey, "")
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Tell the node to put the database in [dataDir/db] unless given in config file
	dbDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.DBPathKey, filepath.Join(dataDir, defaultDBSubdir))
	if err != nil {
		return buildArgsReturn{}, err
	}

	if nodeLogDir == "" {
		nodeLogDir = dataDir
	}
	// Tell the node to put the log directory in [dataDir/logs] unless given in config file
	logsDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.LogsDirKey, filepath.Join(nodeLogDir, defaultLogsSubdir))
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Use random free API port unless given in config file
	apiPort, err := getPort(nodeConfig.Flags, configFile, config.HTTPPortKey)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Use a random free P2P (staking) port unless given in config file
	// Use random free API port unless given in config file
	p2pPort, err := getPort(nodeConfig.Flags, configFile, config.StakingPortKey)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// publicIP from all configs for node
	publicIP, err := getConfigEntry(nodeConfig.Flags, configFile, config.PublicIPKey, constants.IPv4Lookback)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Flags for AvalancheGo
	flags := map[string]string{
		config.NetworkNameKey: fmt.Sprintf("%d", ln.networkID),
		config.DataDirKey:     dataDir,
		config.DBPathKey:      dbDir,
		config.LogsDirKey:     logsDir,
		config.PublicIPKey:    publicIP,
		config.HTTPPortKey:    fmt.Sprintf("%d", apiPort),
		config.StakingPortKey: fmt.Sprintf("%d", p2pPort),
	}
	if !utils.IsPublicNetwork(ln.networkID) {
		flags[config.BootstrapIPsKey] = ln.bootstraps.IPsArg()
		flags[config.BootstrapIDsKey] = ln.bootstraps.IDsArg()
	}

	insideContainer, err := utils.IsInsideDockerContainer()
	if err != nil {
		return buildArgsReturn{}, err
	}
	if insideContainer {
		// mapped localhost requests (eg using -p flag of docker run) are seen as coming from an external IP like 172.17.0.1
		// so inside docker container just accept all requests
		flags[config.HTTPHostKey] = ""
	}

	// Write staking key/cert etc. to disk so the new node can use them,
	// and get flag that point the node to those files
	fileFlags, err := writeFiles(ln.genesis, ln.upgrade, dataDir, nodeConfig)
	if err != nil {
		return buildArgsReturn{}, err
	}
	for k := range fileFlags {
		flags[k] = fileFlags[k]
	}

	// avoid given these again, as apiPort/p2pPort can be dynamic even if given in nodeConfig
	portFlags := set.Set[string]{
		config.HTTPPortKey:    struct{}{},
		config.StakingPortKey: struct{}{},
	}

	// Add flags given in node config.
	// Note these will overwrite existing flags if the same flag is given twice.
	for flagName, flagVal := range nodeConfig.Flags {
		if _, ok := warnFlags[flagName]; ok {
			ln.log.Warn("A provided flag can create conflicts with the runner. The suggestion is to remove this flag", zap.String("flag-name", flagName))
		}
		if portFlags.Contains(flagName) {
			continue
		}
		flags[flagName] = fmt.Sprintf("%v", flagVal)
	}

	// map input flags to the corresponding avago version, making sure that latest flags don't break
	// old avago versions
	flagsForAvagoVersion := getFlagsForAvagoVersion(nodeSemVer, flags)

	configFilePath, err := writeConfigFile(dataDir, nodeConfig, flagsForAvagoVersion)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// create args
	args := []string{
		fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath),
	}

	return buildArgsReturn{
		args:      args,
		publicIP:  publicIP,
		apiPort:   apiPort,
		p2pPort:   p2pPort,
		dataDir:   dataDir,
		dbDir:     dbDir,
		logsDir:   logsDir,
		pluginDir: pluginDir,
		httpHost:  httpHost,
	}, nil
}

// Get AvalancheGo version
func (ln *localNetwork) getNodeSemVer(nodeConfig node.Config) (string, error) {
	nodeVersionOutput, err := ln.nodeProcessCreator.GetNodeVersion(nodeConfig)
	if err != nil {
		return "", fmt.Errorf(
			"couldn't get node version with binary %q: %w",
			nodeConfig.BinaryPath, err,
		)
	}
	re := regexp.MustCompile(`\/([^ ]+)`)
	matchs := re.FindStringSubmatch(nodeVersionOutput)
	if len(matchs) != 2 {
		return "", fmt.Errorf(
			"invalid version output %q for binary %q: version pattern not found",
			nodeVersionOutput, nodeConfig.BinaryPath,
		)
	}
	nodeSemVer := "v" + matchs[1]
	return nodeSemVer, nil
}

// ensure flags are compatible with the running avalanchego version
func getFlagsForAvagoVersion(avagoVersion string, givenFlags map[string]string) map[string]string {
	flags := maps.Clone(givenFlags)
	for _, deprecatedFlagInfo := range deprecatedFlagsSupport {
		if semver.Compare(avagoVersion, deprecatedFlagInfo.Version) < 0 {
			if v, ok := flags[deprecatedFlagInfo.NewName]; ok {
				if v != "" {
					if deprecatedFlagInfo.ValueMap == "parent-dir" {
						v = filepath.Dir(strings.TrimSuffix(v, "/"))
					}
					flags[deprecatedFlagInfo.OldName] = v
				}
				delete(flags, deprecatedFlagInfo.NewName)
			}
		}
	}
	return flags
}

func (ln *localNetwork) GetRootDir() string {
	return ln.rootDir
}

func (ln *localNetwork) GetLogRootDir() string {
	return ln.logRootDir
}
