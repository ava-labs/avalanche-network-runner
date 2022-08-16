package local

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
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
	"github.com/ava-labs/avalanchego/utils/ips"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"golang.org/x/sync/errgroup"
)

const (
	defaultNodeNamePrefix = "node"
	configFileName        = "config.json"
	stakingKeyFileName    = "staking.key"
	stakingCertFileName   = "staking.crt"
	genesisFileName       = "genesis.json"
	stopTimeout           = 30 * time.Second
	healthCheckFreq       = 3 * time.Second
	DefaultNumNodes       = 5
	snapshotPrefix        = "anr-snapshot-"
	rootDirPrefix         = "network-runner-root-data"
	defaultDbSubdir       = "db"
	defaultLogsSubdir     = "logs"
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
	chainConfigSubDir = "chainConfigs"

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
}

var (
	//go:embed default
	embeddedDefaultNetworkConfigDir embed.FS
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
	configsDir, err := fs.Sub(embeddedDefaultNetworkConfigDir, "default")
	if err != nil {
		panic(err)
	}

	// load genesis, updating validation start time
	genesis, err := fs.ReadFile(configsDir, "genesis.json")
	if err != nil {
		panic(err)
	}
	var genesisMap map[string]interface{}
	if err = json.Unmarshal(genesis, &genesisMap); err != nil {
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
	updatedGenesis, err := json.Marshal(genesisMap)
	if err != nil {
		panic(err)
	}

	// load network flags
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
		nextNodeSuffix:     1,
		nodes:              map[string]*localNode{},
		onStopCh:           make(chan struct{}),
		log:                log,
		bootstraps:         beacon.NewSet(),
		newAPIClientF:      newAPIClientF,
		nodeProcessCreator: nodeProcessCreator,
		rootDir:            rootDir,
		snapshotsDir:       snapshotsDir,
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
) (network.Network, error) {
	config := NewDefaultConfig(binaryPath)
	return NewNetwork(log, config, "", "")
}

func copyMapStringInterface(flags map[string]interface{}) map[string]interface{} {
	outFlags := map[string]interface{}{}
	for k, v := range flags {
		outFlags[k] = v
	}
	return outFlags
}

func copyMapStringString(flags map[string]string) map[string]string {
	outFlags := map[string]string{}
	for k, v := range flags {
		outFlags[k] = v
	}
	return outFlags
}

// NewDefaultConfig creates a new default network config
func NewDefaultConfig(binaryPath string) network.Config {
	config := defaultNetworkConfig
	config.BinaryPath = binaryPath
	// Don't overwrite [DefaultNetworkConfig.NodeConfigs]
	config.NodeConfigs = make([]node.Config, len(defaultNetworkConfig.NodeConfigs))
	copy(config.NodeConfigs, defaultNetworkConfig.NodeConfigs)
	// copy maps
	config.ChainConfigFiles = copyMapStringString(config.ChainConfigFiles)
	config.Flags = copyMapStringInterface(config.Flags)
	for i := range config.NodeConfigs {
		config.NodeConfigs[i].Flags = copyMapStringInterface(config.NodeConfigs[i].Flags)
	}
	return config
}

// NewDefaultConfigNNodes creates a new default network config, with an arbitrary number of nodes
func NewDefaultConfigNNodes(binaryPath string, numNodes uint32) (network.Config, error) {
	netConfig := NewDefaultConfig(binaryPath)
	if int(numNodes) > len(netConfig.NodeConfigs) {
		toAdd := int(numNodes) - len(netConfig.NodeConfigs)
		refNodeConfig := netConfig.NodeConfigs[0]
		for i := 0; i < toAdd; i++ {
			nodeConfig := refNodeConfig
			stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
			if err != nil {
				return netConfig, fmt.Errorf("couldn't generate staking Cert/Key: %w", err)
			}
			nodeConfig.StakingKey = string(stakingKey)
			nodeConfig.StakingCert = string(stakingCert)
			// replace api port in refNodeConfig.ConfigFile
			apiPort, err := getFreePort()
			if err != nil {
				return netConfig, fmt.Errorf("couldn't get free API port: %w", err)
			}
			nodeConfig.Flags = map[string]interface{}{
				config.HTTPPortKey: int(apiPort),
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
	ln.log.Info("creating network", zap.Int("nodes", len(networkConfig.NodeConfigs)))

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
			return fmt.Errorf("error adding node %s: %s", nodeConfig.Name, err)
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

	if err := ln.setNodeName(&nodeConfig); err != nil {
		return nil, err
	}

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

	nodeData, err := ln.buildFlags(configFile, nodeDir, &nodeConfig)
	if err != nil {
		return nil, err
	}

	// Parse this node's ID
	nodeID, err := utils.ToNodeID([]byte(nodeConfig.StakingKey), []byte(nodeConfig.StakingCert))
	if err != nil {
		return nil, fmt.Errorf("couldn't get node ID: %w", err)
	}

	// Start the AvalancheGo node and pass it the flags defined above
	nodeProcess, err := ln.nodeProcessCreator.NewNodeProcess(nodeConfig, nodeData.flags...)
	if err != nil {
		return nil, fmt.Errorf(
			"couldn't create new node process with binary %q and flags %v: %w",
			nodeConfig.BinaryPath, nodeData.flags, err,
		)
	}

	ln.log.Info(
		"adding node",
		zap.String("name", nodeConfig.Name),
		zap.String("tmpDir", nodeDir),
		zap.String("logDir", nodeData.logsDir),
		zap.String("dbDir", nodeData.dbDir),
		zap.Uint16("p2pPort", nodeData.p2pPort),
		zap.Uint16("apiPort", nodeData.apiPort),
	)

	ln.log.Debug(
		"starting node",
		zap.String("name", nodeConfig.Name),
		zap.String("binaryPath", nodeConfig.BinaryPath),
		zap.Strings("flags", nodeData.flags),
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
		dbDir:         nodeData.dbDir,
		logsDir:       nodeData.logsDir,
		config:        nodeConfig,
		buildDir:      nodeData.buildDir,
		httpHost:      nodeData.httpHost,
		attachedPeers: map[string]peer.Peer{},
	}
	ln.nodes[node.name] = node
	// If this node is a beacon, add its IP/ID to the beacon lists.
	// Note that we do this *after* we set this node's bootstrap IPs/IDs
	// so this node won't try to use itself as a beacon.
	if nodeConfig.IsBeacon {
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
	ln.log.Info("checking local network healthiness, nodes: %d", len(ln.nodes))

	// Return unhealthy if the network is stopped
	if ln.stopCalled() {
		return network.ErrStopped
	}

	// Derive a new context that's cancelled when Stop is called,
	// so that we calls to Healthy() below immediately return.
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

	names := make([]string, len(ln.nodes))
	i := 0
	for name := range ln.nodes {
		names[i] = name
		i++
	}
	return names, nil
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

	// If the node wasn't a beacon, we don't care
	_ = ln.bootstraps.RemoveByID(node.nodeID)

	delete(ln.nodes, nodeName)
	// cchain eth api uses a websocket connection and must be closed before stopping the node,
	// to avoid errors logs at client
	node.client.CChainEthAPI().Close()
	if exitCode := node.process.Stop(ctx); exitCode != 0 {
		return fmt.Errorf("node %q exited with exit code: %d", nodeName, exitCode)
	}
	return nil
}

// Restart [nodeName] using the same config, optionally changing [binaryPath],
// [buildDir], [whitelistedSubnets]
func (ln *localNetwork) RestartNode(
	ctx context.Context,
	nodeName string,
	binaryPath string,
	whitelistedSubnets string,
	chainConfigs map[string]string,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	node, ok := ln.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}

	nodeConfig := node.GetConfig()

	if binaryPath != "" {
		nodeConfig.BinaryPath = binaryPath
		nodeConfig.Flags[config.BuildDirKey] = filepath.Dir(binaryPath)
	}

	if whitelistedSubnets != "" {
		nodeConfig.Flags[config.WhitelistedSubnetsKey] = whitelistedSubnets
	}

	// keep same ports, dbdir in node flags
	nodeConfig.Flags[config.DBPathKey] = node.GetDbDir()
	nodeConfig.Flags[config.HTTPPortKey] = int(node.GetAPIPort())
	nodeConfig.Flags[config.StakingPortKey] = int(node.GetP2PPort())
	// apply chain configs
	for k, v := range chainConfigs {
		nodeConfig.ChainConfigFiles[k] = v
	}

	if err := ln.removeNode(ctx, nodeName); err != nil {
		return err
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
	if _, ok := ln.nodes[nodeConfig.Name]; ok {
		return fmt.Errorf("repeated node name %q", nodeConfig.Name)
	}
	return nil
}

type buildFlagsReturn struct {
	flags    []string
	apiPort  uint16
	p2pPort  uint16
	dbDir    string
	logsDir  string
	buildDir string
	httpHost string
}

// buildFlags returns the:
// 1) Flags
// 2) API port
// 3) P2P port
// of the node being added with config [nodeConfig], config file [configFile],
// and directory at [nodeDir].
// [nodeConfig.Flags] must not be nil
func (ln *localNetwork) buildFlags(
	configFile map[string]interface{},
	nodeDir string,
	nodeConfig *node.Config,
) (buildFlagsReturn, error) {
	// httpHost from all configs for node
	httpHost, err := getConfigEntry(nodeConfig.Flags, configFile, config.HTTPHostKey, "")
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// buildDir from all configs for node
	buildDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.BuildDirKey, "")
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// Tell the node to put the database in [nodeDir] unless given in config file
	dbDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.DBPathKey, filepath.Join(nodeDir, defaultDbSubdir))
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// Tell the node to put the log directory in [nodeDir/logs] unless given in config file
	logsDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.LogsDirKey, filepath.Join(nodeDir, defaultLogsSubdir))
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// Use random free API port unless given in config file
	apiPort, err := getPort(nodeConfig.Flags, configFile, config.HTTPPortKey)
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// Use a random free P2P (staking) port unless given in config file
	// Use random free API port unless given in config file
	p2pPort, err := getPort(nodeConfig.Flags, configFile, config.StakingPortKey)
	if err != nil {
		return buildFlagsReturn{}, err
	}

	// Flags for AvalancheGo
	flags := []string{
		fmt.Sprintf("--%s=%d", config.NetworkNameKey, ln.networkID),
		fmt.Sprintf("--%s=%s", config.DBPathKey, dbDir),
		fmt.Sprintf("--%s=%s", config.LogsDirKey, logsDir),
		fmt.Sprintf("--%s=%d", config.HTTPPortKey, apiPort),
		fmt.Sprintf("--%s=%d", config.StakingPortKey, p2pPort),
		fmt.Sprintf("--%s=%s", config.BootstrapIPsKey, ln.bootstraps.IPsArg()),
		fmt.Sprintf("--%s=%s", config.BootstrapIDsKey, ln.bootstraps.IDsArg()),
	}
	// Write staking key/cert etc. to disk so the new node can use them,
	// and get flag that point the node to those files
	fileFlags, err := writeFiles(ln.genesis, nodeDir, nodeConfig)
	if err != nil {
		return buildFlagsReturn{}, err
	}
	flags = append(flags, fileFlags...)

	// Add flags given in node config.
	// Note these will overwrite existing flags if the same flag is given twice.
	for flagName, flagVal := range nodeConfig.Flags {
		if _, ok := warnFlags[flagName]; ok {
			ln.log.Warn("A provided flag can create conflicts with the runner. The suggestion is to remove this flag", zap.String("flagName", flagName))
		}
		flags = append(flags, fmt.Sprintf("--%s=%v", flagName, flagVal))
	}

	return buildFlagsReturn{
		flags:    flags,
		apiPort:  apiPort,
		p2pPort:  p2pPort,
		dbDir:    dbDir,
		logsDir:  logsDir,
		buildDir: buildDir,
		httpHost: httpHost,
	}, nil
}
