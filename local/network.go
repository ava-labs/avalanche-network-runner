package local

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
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
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/beacon"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/ips"
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
	stakingKeyFileName        = "staking.key"
	stakingCertFileName       = "staking.crt"
	stakingSigningKeyFileName = "signer.key"
	genesisFileName           = "genesis.json"
	stopTimeout               = 30 * time.Second
	healthCheckFreq           = 3 * time.Second
	DefaultNumNodes           = 5
	snapshotPrefix            = "anr-snapshot-"
	rootDirPrefix             = "network-runner-root-data"
	defaultDBSubdir           = "db"
	defaultLogsSubdir         = "logs"
	// difference between unlock schedule locktime and startime in original genesis
	genesisLocktimeStartimeDelta = 2836800
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
	// logs, databases, etc.
	rootDir string
	// directory where networks can be persistently saved
	snapshotsDir string
	// flags to apply to all nodes per default
	flags map[string]interface{}
	// binary path to use per default
	binaryPath string
	// chain config files to use per default
	chainConfigFiles map[string]string
	// upgrade config files to use per default
	upgradeConfigFiles map[string]string
	// subnet config files to use per default
	subnetConfigFiles map[string]string
	// if true, for ports given in conf that are already taken, assign new random ones
	reassignPortsIfUsed bool
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
	// Pre-defined network configuration.
	// [defaultNetworkConfig] should not be modified.
	// TODO add method Copy() to network.Config to prevent
	// accidental overwriting
	defaultNetworkConfig network.Config
	// snapshots directory
	defaultSnapshotsDir string
)

// populate default network config from embedded default directory
func init() {
	// load genesis, updating validation start time
	genesisMap, err := network.LoadLocalGenesis()
	if err != nil {
		panic(err)
	}
	// load deprecated avago flags support information
	if err = json.Unmarshal(deprecatedFlagsSupportBytes, &deprecatedFlagsSupport); err != nil {
		panic(err)
	}

	startTime := time.Now().Unix()
	lockTime := startTime + genesisLocktimeStartimeDelta
	genesisMap["startTime"] = float64(startTime)
	allocations, ok := genesisMap["allocations"].([]interface{})
	if !ok {
		panic(errors.New("could not get allocations in genesis"))
	}
	for _, allocIntf := range allocations {
		alloc, ok := allocIntf.(map[string]interface{})
		if !ok {
			panic(fmt.Errorf("unexpected type for allocation in genesis. got %T", allocIntf))
		}
		unlockSchedule, ok := alloc["unlockSchedule"].([]interface{})
		if !ok {
			panic(errors.New("could not get unlockSchedule in allocation"))
		}
		for _, schedIntf := range unlockSchedule {
			sched, ok := schedIntf.(map[string]interface{})
			if !ok {
				panic(fmt.Errorf("unexpected type for unlockSchedule elem in genesis. got %T", schedIntf))
			}
			if _, ok := sched["locktime"]; ok {
				sched["locktime"] = float64(lockTime)
			}
		}
	}

	// now we can marshal the *whole* thing into bytes
	updatedGenesis, err := json.Marshal(genesisMap)
	if err != nil {
		panic(err)
	}

	// load network flags
	configsDir, err := fs.Sub(embeddedDefaultNetworkConfigDir, "default")
	if err != nil {
		panic(err)
	}
	flagsBytes, err := fs.ReadFile(configsDir, "flags.json")
	if err != nil {
		panic(err)
	}
	flags := map[string]interface{}{}
	if err = json.Unmarshal(flagsBytes, &flags); err != nil {
		panic(err)
	}

	// load chain config
	cChainConfig, err := fs.ReadFile(configsDir, "cchain_config.json")
	if err != nil {
		panic(err)
	}

	defaultNetworkConfig = network.Config{
		NodeConfigs: make([]node.Config, DefaultNumNodes),
		Flags:       flags,
		Genesis:     string(updatedGenesis),
		ChainConfigFiles: map[string]string{
			"C": string(cChainConfig),
		},
		UpgradeConfigFiles: map[string]string{},
		SubnetConfigFiles:  map[string]string{},
	}

	for i := 0; i < len(defaultNetworkConfig.NodeConfigs); i++ {
		flagsBytes, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/flags.json", i+1))
		if err != nil {
			panic(err)
		}
		flags := map[string]interface{}{}
		if err = json.Unmarshal(flagsBytes, &flags); err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].Flags = flags
		stakingKey, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.key", i+1))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].StakingKey = string(stakingKey)
		stakingCert, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.crt", i+1))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].StakingCert = string(stakingCert)
		stakingSigningKey, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/signer.key", i+1))
		if err != nil {
			panic(err)
		}
		encodedStakingSigningKey := base64.StdEncoding.EncodeToString(stakingSigningKey)
		defaultNetworkConfig.NodeConfigs[i].StakingSigningKey = encodedStakingSigningKey
		defaultNetworkConfig.NodeConfigs[i].IsBeacon = true
	}

	// create default snapshots dir
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	defaultSnapshotsDir = filepath.Join(usr.HomeDir, snapshotsRelPath)
}

// NewNetwork returns a new network that uses the given log.
// Files (e.g. logs, databases) default to being written at directory [rootDir].
// If there isn't a directory at [dir] one will be created.
// If len([dir]) == 0, files will be written underneath a new temporary directory.
// Snapshots are saved to snapshotsDir, defaults to defaultSnapshotsDir if not given
func NewNetwork(
	log logging.Logger,
	networkConfig network.Config,
	rootDir string,
	snapshotsDir string,
	reassignPortsIfUsed bool,
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
		snapshotsDir,
		reassignPortsIfUsed,
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
	snapshotsDir string,
	reassignPortsIfUsed bool,
) (*localNetwork, error) {
	var err error
	if rootDir == "" {
		rootDir = filepath.Join(os.TempDir(), rootDirPrefix)
		rootDir, err = utils.MkDirWithTimestamp(rootDir)
		if err != nil {
			return nil, err
		}
	}
	if snapshotsDir == "" {
		snapshotsDir = defaultSnapshotsDir
	}
	// create the snapshots dir if not present
	err = os.MkdirAll(snapshotsDir, os.ModePerm)
	if err != nil {
		return nil, err
	}
	// Create the network
	net := &localNetwork{
		nextNodeSuffix:      1,
		nodes:               map[string]*localNode{},
		onStopCh:            make(chan struct{}),
		log:                 log,
		bootstraps:          beacon.NewSet(),
		newAPIClientF:       newAPIClientF,
		nodeProcessCreator:  nodeProcessCreator,
		rootDir:             rootDir,
		snapshotsDir:        snapshotsDir,
		reassignPortsIfUsed: reassignPortsIfUsed,
	}
	return net, nil
}

// NewDefaultNetwork returns a new network using a pre-defined
// network configuration.
// The following addresses are pre-funded:
// X-Chain Address 1:     X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// X-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// X-Chain Address 2:     X-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// X-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
// P-Chain Address 1:     P-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// P-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// P-Chain Address 2:     P-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// P-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
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
) (network.Network, error) {
	config := NewDefaultConfig(binaryPath)
	return NewNetwork(log, config, "", "", reassignPortsIfUsed)
}

// NewDefaultConfig creates a new default network config
func NewDefaultConfig(binaryPath string) network.Config {
	config := defaultNetworkConfig
	config.BinaryPath = binaryPath
	// Don't overwrite [DefaultNetworkConfig.NodeConfigs]
	config.NodeConfigs = make([]node.Config, len(defaultNetworkConfig.NodeConfigs))
	copy(config.NodeConfigs, defaultNetworkConfig.NodeConfigs)
	// copy maps
	config.ChainConfigFiles = maps.Clone(config.ChainConfigFiles)
	config.Flags = maps.Clone(config.Flags)
	for i := range config.NodeConfigs {
		config.NodeConfigs[i].Flags = maps.Clone(config.NodeConfigs[i].Flags)
	}
	return config
}

// NewDefaultConfigNNodes creates a new default network config, with an arbitrary number of nodes
func NewDefaultConfigNNodes(binaryPath string, numNodes uint32) (network.Config, error) {
	netConfig := NewDefaultConfig(binaryPath)
	if int(numNodes) > len(netConfig.NodeConfigs) {
		toAdd := int(numNodes) - len(netConfig.NodeConfigs)
		refNodeConfig := netConfig.NodeConfigs[len(netConfig.NodeConfigs)-1]
		refAPIPortIntf, ok := refNodeConfig.Flags[config.HTTPPortKey]
		if !ok {
			return netConfig, fmt.Errorf("could not get last standard api port from config")
		}
		refAPIPort, ok := refAPIPortIntf.(float64)
		if !ok {
			return netConfig, fmt.Errorf("expected float64 for last standard api port, got %T", refAPIPortIntf)
		}
		refStakingPortIntf, ok := refNodeConfig.Flags[config.StakingPortKey]
		if !ok {
			return netConfig, fmt.Errorf("could not get last standard staking port from config")
		}
		refStakingPort, ok := refStakingPortIntf.(float64)
		if !ok {
			return netConfig, fmt.Errorf("expected float64 for last standard api port, got %T", refStakingPortIntf)
		}
		for i := 0; i < toAdd; i++ {
			nodeConfig := refNodeConfig
			stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
			if err != nil {
				return netConfig, fmt.Errorf("couldn't generate staking Cert/Key: %w", err)
			}
			nodeConfig.StakingKey = string(stakingKey)
			nodeConfig.StakingCert = string(stakingCert)
			// replace ports
			nodeConfig.Flags = map[string]interface{}{
				config.HTTPPortKey:    int(refAPIPort) + (i+1)*2,
				config.StakingPortKey: int(refStakingPort) + (i+1)*2,
			}
			netConfig.NodeConfigs = append(netConfig.NodeConfigs, nodeConfig)
		}
	}
	if int(numNodes) < len(netConfig.NodeConfigs) {
		netConfig.NodeConfigs = netConfig.NodeConfigs[:numNodes]
	}
	return netConfig, nil
}

func (ln *localNetwork) loadConfig(ctx context.Context, networkConfig network.Config) error {
	if err := networkConfig.Validate(); err != nil {
		return fmt.Errorf("config failed validation: %w", err)
	}
	ln.log.Info("creating network", zap.Int("node-num", len(networkConfig.NodeConfigs)))

	ln.genesis = []byte(networkConfig.Genesis)

	var err error
	ln.networkID, err = utils.NetworkIDFromGenesis([]byte(networkConfig.Genesis))
	if err != nil {
		return fmt.Errorf("couldn't get network ID from genesis: %w", err)
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

	return nil
}

// See network.Network
func (ln *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if ln.stopCalled() {
		return nil, network.ErrStopped
	}

	return ln.addNode(nodeConfig)
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
	addNetworkFlags(ln.log, ln.flags, nodeConfig.Flags)

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

	nodeDir, err := makeNodeDir(ln.log, ln.rootDir, nodeConfig.Name)
	if err != nil {
		return nil, err
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

	nodeData, err := ln.buildArgs(nodeSemVer, configFile, nodeDir, &nodeConfig)
	if err != nil {
		return nil, err
	}

	// Parse this node's ID
	nodeID, err := utils.ToNodeID([]byte(nodeConfig.StakingKey), []byte(nodeConfig.StakingCert))
	if err != nil {
		return nil, fmt.Errorf("couldn't get node ID: %w", err)
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
		client:        ln.newAPIClientF("localhost", nodeData.apiPort),
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
	// If this node is a beacon, add its IP/ID to the beacon lists.
	// Note that we do this *after* we set this node's bootstrap IPs/IDs
	// so this node won't try to use itself as a beacon.
	if !isPausedNode && nodeConfig.IsBeacon {
		err = ln.bootstraps.Add(beacon.New(nodeID, ips.IPPort{
			IP:   net.IPv6loopback,
			Port: nodeData.p2pPort,
		}))
	}
	return node, err
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
				health, err := node.client.HealthAPI().Health(ctx)
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
	return ln.removeNode(ctx, nodeName)
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
	return ln.pauseNode(ctx, nodeName)
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

	return ln.resumeNode(
		ctx,
		nodeName,
	)
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

	return ln.restartNode(
		ctx,
		nodeName,
		binaryPath,
		pluginDir,
		trackSubnets,
		chainConfigs,
		upgradeConfigs,
		subnetConfigs,
	)
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

	// Tell the node to put the log directory in [dataDir/logs] unless given in config file
	logsDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.LogsDirKey, filepath.Join(dataDir, defaultLogsSubdir))
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Use random free API port unless given in config file
	apiPort, err := getPort(nodeConfig.Flags, configFile, config.HTTPPortKey, ln.reassignPortsIfUsed)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Use a random free P2P (staking) port unless given in config file
	// Use random free API port unless given in config file
	p2pPort, err := getPort(nodeConfig.Flags, configFile, config.StakingPortKey, ln.reassignPortsIfUsed)
	if err != nil {
		return buildArgsReturn{}, err
	}

	// Flags for AvalancheGo
	flags := map[string]string{
		config.NetworkNameKey:  fmt.Sprintf("%d", ln.networkID),
		config.DataDirKey:      dataDir,
		config.DBPathKey:       dbDir,
		config.LogsDirKey:      logsDir,
		config.HTTPPortKey:     fmt.Sprintf("%d", apiPort),
		config.StakingPortKey:  fmt.Sprintf("%d", p2pPort),
		config.BootstrapIPsKey: ln.bootstraps.IPsArg(),
		config.BootstrapIDsKey: ln.bootstraps.IDsArg(),
	}

	// Write staking key/cert etc. to disk so the new node can use them,
	// and get flag that point the node to those files
	fileFlags, err := writeFiles(ln.networkID, ln.genesis, dataDir, nodeConfig)
	if err != nil {
		return buildArgsReturn{}, err
	}
	for k := range fileFlags {
		flags[k] = fileFlags[k]
	}

	// avoid given these again, as apiPort/p2pPort can be dynamic even if given in nodeConfig
	portFlags := set.Set[string]{
		config.HTTPPortKey:    {},
		config.StakingPortKey: {},
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

	// create args
	args := []string{}
	for k, v := range flagsForAvagoVersion {
		args = append(args, fmt.Sprintf("--%s=%s", k, v))
	}

	return buildArgsReturn{
		args:      args,
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
