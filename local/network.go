package local

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/beacon"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/ips"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	dircopy "github.com/otiai10/copy"
	"go.uber.org/zap"
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
	rootDirPrefix         = "avalanche-network-runner-"
	defaultDbSubdir       = "db"
	defaultLogsSubdir     = "logs"
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
	// Flags to apply to all nodes if not present
	flags map[string]interface{}
	// directory where networks can be persistently saved
	snapshotsDir string
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

	defaultNetworkConfig = network.Config{
		NodeConfigs: make([]node.Config, DefaultNumNodes),
	}

	genesis, err := fs.ReadFile(configsDir, "genesis.json")
	if err != nil {
		panic(err)
	}
	defaultNetworkConfig.Genesis = string(genesis)

	for i := 0; i < len(defaultNetworkConfig.NodeConfigs); i++ {
		configFile, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/config.json", i))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].ConfigFile = string(configFile)
		stakingKey, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.key", i))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].StakingKey = string(stakingKey)
		stakingCert, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.crt", i))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].StakingCert = string(stakingCert)
		cChainConfig, err := fs.ReadFile(configsDir, fmt.Sprintf("node%d/cchain_config.json", i))
		if err != nil {
			panic(err)
		}
		defaultNetworkConfig.NodeConfigs[i].ChainConfigFiles = map[string]string{
			"C": string(cChainConfig),
		}
		defaultNetworkConfig.NodeConfigs[i].IsBeacon = true
	}

	// create default snapshots dir
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}
	defaultSnapshotsDir = filepath.Join(usr.HomeDir, snapshotsRelPath)
}

// NodeProcessCreator is an interface for new node process creation
type NodeProcessCreator interface {
	NewNodeProcess(config node.Config, args ...string) (NodeProcess, error)
}

type nodeProcessCreator struct {
	// If this node's stdout or stderr are redirected, [colorPicker] determines
	// the color of logs printed to stdout and/or stderr
	colorPicker utils.ColorPicker
	// If this node's stdout is redirected, it will be to here.
	// In practice this is usually os.Stdout, but for testing can be replaced.
	stdout io.Writer
	// If this node's stderr is redirected, it will be to here.
	// In practice this is usually os.Stderr, but for testing can be replaced.
	stderr io.Writer
}

// NewNodeProcess creates a new process of the passed binary
// If the config has redirection set to `true` for either StdErr or StdOut,
// the output will be redirected and colored
func (npc *nodeProcessCreator) NewNodeProcess(config node.Config, args ...string) (NodeProcess, error) {
	// Start the AvalancheGo node and pass it the flags defined above
	cmd := exec.Command(config.BinaryPath, args...)
	// assign a new color to this process (might not be used if the config isn't set for it)
	color := npc.colorPicker.NextColor()
	// Optionally redirect stdout and stderr
	if config.RedirectStdout {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("couldn't create stdout pipe: %s", err)
		}
		// redirect stdout and assign a color to the text
		utils.ColorAndPrepend(stdout, npc.stdout, config.Name, color)
	}
	if config.RedirectStderr {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("couldn't create stderr pipe: %s", err)
		}
		// redirect stderr and assign a color to the text
		utils.ColorAndPrepend(stderr, npc.stderr, config.Name, color)
	}
	return &nodeProcessImpl{cmd: cmd}, nil
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
		rootDir, err = os.MkdirTemp("", fmt.Sprintf("%s-*", rootDirPrefix))
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

// NewNetwork returns a new network from the given snapshot
func NewNetworkFromSnapshot(
	log logging.Logger,
	snapshotName string,
	rootDir string,
	snapshotsDir string,
	binaryPath string,
	buildDir string,
) (network.Network, error) {
	net, err := newNetwork(
		log,
		api.NewAPIClient,
		&nodeProcessCreator{
			colorPicker: utils.NewColorPicker(),
			stdout:      os.Stdout,
			stderr:      os.Stderr,
		},
		rootDir,
		snapshotsDir,
	)
	if err != nil {
		return net, err
	}
	err = net.loadSnapshot(context.Background(), snapshotName, binaryPath, buildDir)
	return net, err
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

// NewDefaultConfig creates a new default network config
func NewDefaultConfig(binaryPath string) network.Config {
	config := defaultNetworkConfig
	// Don't overwrite [DefaultNetworkConfig.NodeConfigs]
	config.NodeConfigs = make([]node.Config, len(defaultNetworkConfig.NodeConfigs))
	copy(config.NodeConfigs, defaultNetworkConfig.NodeConfigs)
	for i := 0; i < len(config.NodeConfigs); i++ {
		config.NodeConfigs[i].BinaryPath = binaryPath
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
	ln.log.Info("creating network with %d nodes", len(networkConfig.NodeConfigs))

	ln.genesis = []byte(networkConfig.Genesis)

	var err error
	ln.networkID, err = utils.NetworkIDFromGenesis([]byte(networkConfig.Genesis))
	if err != nil {
		return fmt.Errorf("couldn't get network ID from genesis: %w", err)
	}

	ln.flags = networkConfig.Flags

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
				ln.log.Debug("error stopping network: %s", err)
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
		nodeConfig.Flags = make(map[string]interface{})
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

	flags, apiPort, p2pPort, dbDir, logsDir, err := ln.buildFlags(configFile, nodeDir, &nodeConfig)
	if err != nil {
		return nil, err
	}

	// Parse this node's ID
	nodeID, err := utils.ToNodeID([]byte(nodeConfig.StakingKey), []byte(nodeConfig.StakingCert))
	if err != nil {
		return nil, fmt.Errorf("couldn't get node ID: %w", err)
	}

	// Start the AvalancheGo node and pass it the flags defined above
	nodeProcess, err := ln.nodeProcessCreator.NewNodeProcess(nodeConfig, flags...)
	if err != nil {
		return nil, fmt.Errorf("couldn't create new node process: %s", err)
	}
	ln.log.Debug("starting node %q with \"%s %s\"", nodeConfig.Name, nodeConfig.BinaryPath, flags)
	if err := nodeProcess.Start(); err != nil {
		return nil, fmt.Errorf("could not execute cmd \"%s %s\": %w", nodeConfig.BinaryPath, flags, err)
	}

	// Create a wrapper for this node so we can reference it later
	node := &localNode{
		name:        nodeConfig.Name,
		nodeID:      nodeID,
		networkID:   ln.networkID,
		client:      ln.newAPIClientF("localhost", apiPort),
		process:     nodeProcess,
		apiPort:     apiPort,
		p2pPort:     p2pPort,
		getConnFunc: defaultGetConnFunc,
		dbDir:       dbDir,
		logsDir:     logsDir,
		config:      nodeConfig,
	}
	ln.nodes[node.name] = node
	// If this node is a beacon, add its IP/ID to the beacon lists.
	// Note that we do this *after* we set this node's bootstrap IPs/IDs
	// so this node won't try to use itself as a beacon.
	if nodeConfig.IsBeacon {
		err = ln.bootstraps.Add(beacon.New(nodeID, ips.IPPort{
			IP:   net.IPv6loopback,
			Port: p2pPort,
		}))
	}
	return node, err
}

// See network.Network
func (ln *localNetwork) Healthy(ctx context.Context) error {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	zap.L().Info("checking local network healthiness", zap.Int("nodes", len(ln.nodes)))

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
		errGr.Go(func() error {
			// Every [healthCheckFreq], query node for health status.
			// Do this until ctx timeout or network closed.
			for {
				health, err := node.client.HealthAPI().Health(ctx)
				if err == nil && health.Healthy {
					ln.log.Debug("node %q became healthy", node.name)
					return nil
				}
				select {
				case <-ctx.Done():
					return fmt.Errorf("node %q failed to become healthy within timeout, or network stopped", node.GetName())
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
		return nil, fmt.Errorf("node %q not found in network", nodeName)
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
	ctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	errs := wrappers.Errs{}
	for nodeName := range ln.nodes {
		select {
		case <-ctx.Done():
			// In practice we'll probably never time out here,
			// and the caller probably won't cancel a call
			// to stop(), but we include this to respect the
			// network.Network interface.
			return ctx.Err()
		default:
		}
		if err := ln.removeNode(nodeName); err != nil {
			ln.log.Error("error stopping node %q: %s", nodeName, err)
			errs.Add(err)
		}
	}
	ln.log.Info("done stopping network")
	return errs.Err
}

// Sends a SIGTERM to the given node and removes it from this network.
func (ln *localNetwork) RemoveNode(nodeName string) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	if ln.stopCalled() {
		return network.ErrStopped
	}
	return ln.removeNode(nodeName)
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) removeNode(nodeName string) error {
	ln.log.Debug("removing node %q", nodeName)
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
	if err := node.process.Stop(); err != nil {
		return fmt.Errorf("error sending SIGTERM to node %s: %w", nodeName, err)
	}
	if err := node.process.Wait(); err != nil {
		return fmt.Errorf("node %q stopped with error: %w", nodeName, err)
	}
	return nil
}

// Save network snapshot
// Network is stopped in order to do a safe preservation
func (ln *localNetwork) SaveSnapshot(ctx context.Context, snapshotName string) (string, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	if ln.stopCalled() {
		return "", network.ErrStopped
	}
	if len(snapshotName) == 0 {
		return "", fmt.Errorf("invalid snapshotName %q", snapshotName)
	}
	// check if snapshot already exists
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
	_, err := os.Stat(snapshotDir)
	if err == nil {
		return "", fmt.Errorf("snapshot %q already exists", snapshotName)
	}
	// keep copy of node info that will be removed by stop
	nodesConfig := map[string]node.Config{}
	nodesDbDir := map[string]string{}
	for nodeName, node := range ln.nodes {
		nodeConfig := node.config
		// depending on how the user generated the config, different nodes config flags
		// may point to the same map, so we made a copy to avoid always modifying the same value
		nodeConfigFlags := make(map[string]interface{})
		for fk, fv := range nodeConfig.Flags {
			nodeConfigFlags[fk] = fv
		}
		nodeConfig.Flags = nodeConfigFlags
		nodesConfig[nodeName] = nodeConfig
		nodesDbDir[nodeName] = node.GetDbDir()
	}
	// we change nodeConfig.Flags so as to preserve in snapshot the current node ports
	for nodeName, nodeConfig := range nodesConfig {
		nodeConfig.Flags[config.HTTPPortKey] = ln.nodes[nodeName].GetAPIPort()
		nodeConfig.Flags[config.StakingPortKey] = ln.nodes[nodeName].GetP2PPort()
	}
	// make copy of network flags
	networkConfigFlags := make(map[string]interface{})
	for fk, fv := range ln.flags {
		networkConfigFlags[fk] = fv
	}
	// remove all log dir references
	delete(networkConfigFlags, config.LogsDirKey)
	for nodeName, nodeConfig := range nodesConfig {
		nodeConfig.ConfigFile, err = utils.SetJSONKey(nodeConfig.ConfigFile, config.LogsDirKey, "")
		if err != nil {
			return "", err
		}
		delete(nodeConfig.Flags, config.LogsDirKey)
		nodesConfig[nodeName] = nodeConfig
	}

	// stop network to safely save snapshot
	if err := ln.stop(ctx); err != nil {
		return "", err
	}
	// create main snapshot dirs
	snapshotDbDir := filepath.Join(filepath.Join(snapshotDir, defaultDbSubdir))
	err = os.MkdirAll(snapshotDbDir, os.ModePerm)
	if err != nil {
		return "", err
	}
	// save db
	for _, nodeConfig := range nodesConfig {
		sourceDbDir, ok := nodesDbDir[nodeConfig.Name]
		if !ok {
			return "", fmt.Errorf("failure obtaining db path for node %q", nodeConfig.Name)
		}
		sourceDbDir = filepath.Join(sourceDbDir, constants.NetworkName(ln.networkID))
		targetDbDir := filepath.Join(filepath.Join(snapshotDbDir, nodeConfig.Name), constants.NetworkName(ln.networkID))
		if err := dircopy.Copy(sourceDbDir, targetDbDir); err != nil {
			return "", fmt.Errorf("failure saving node %q db dir: %w", nodeConfig.Name, err)
		}
	}
	// save network conf
	networkConfig := network.Config{
		Genesis:     string(ln.genesis),
		Flags:       networkConfigFlags,
		NodeConfigs: []node.Config{},
	}
	for _, nodeConfig := range nodesConfig {
		// no need to save this, will be generated automatically on snapshot load
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfigJSON, err := json.MarshalIndent(networkConfig, "", "    ")
	if err != nil {
		return "", err
	}
	err = createFileAndWrite(filepath.Join(snapshotDir, "network.json"), networkConfigJSON)
	if err != nil {
		return "", err
	}
	return snapshotDir, nil
}

// start network from snapshot
func (ln *localNetwork) loadSnapshot(
	ctx context.Context,
	snapshotName string,
	binaryPath string,
	buildDir string,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
	snapshotDbDir := filepath.Join(filepath.Join(snapshotDir, defaultDbSubdir))
	_, err := os.Stat(snapshotDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("snapshot %q does not exists", snapshotName)
		} else {
			return fmt.Errorf("failure accessing snapshot %q: %w", snapshotName, err)
		}
	}
	// load network config
	networkConfigJSON, err := os.ReadFile(filepath.Join(snapshotDir, "network.json"))
	if err != nil {
		return fmt.Errorf("failure reading network config file from snapshot: %w", err)
	}
	networkConfig := network.Config{}
	err = json.Unmarshal(networkConfigJSON, &networkConfig)
	if err != nil {
		return fmt.Errorf("failure unmarshaling network config from snapshot: %w", err)
	}
	// load db
	for _, nodeConfig := range networkConfig.NodeConfigs {
		sourceDbDir := filepath.Join(snapshotDbDir, nodeConfig.Name)
		targetDbDir := filepath.Join(filepath.Join(ln.rootDir, nodeConfig.Name), defaultDbSubdir)
		if err := dircopy.Copy(sourceDbDir, targetDbDir); err != nil {
			return fmt.Errorf("failure loading node %q db dir: %w", nodeConfig.Name, err)
		}
		nodeConfig.Flags[config.DBPathKey] = targetDbDir
	}
	// replace binary path
	if binaryPath != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].BinaryPath = binaryPath
		}
	}
	// replace build dir
	if buildDir != "" {
		for i := range networkConfig.NodeConfigs {
			networkConfig.NodeConfigs[i].Flags[config.BuildDirKey] = buildDir
		}
	}
	return ln.loadConfig(ctx, networkConfig)
}

// Remove network snapshot
func (ln *localNetwork) RemoveSnapshot(snapshotName string) error {
	snapshotDir := filepath.Join(ln.snapshotsDir, snapshotPrefix+snapshotName)
	_, err := os.Stat(snapshotDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("snapshot %q does not exists", snapshotName)
		} else {
			return fmt.Errorf("failure accessing snapshot %q: %w", snapshotName, err)
		}
	}
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("failure removing snapshot path %q: %w", snapshotDir, err)
	}
	return nil
}

// Get network snapshots
func (ln *localNetwork) GetSnapshotNames() ([]string, error) {
	_, err := os.Stat(ln.snapshotsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("snapshots dir %q does not exists", ln.snapshotsDir)
		} else {
			return nil, fmt.Errorf("failure accessing snapshots dir %q: %w", ln.snapshotsDir, err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(ln.snapshotsDir, snapshotPrefix+"*"))
	if err != nil {
		return nil, err
	}
	snapshots := []string{}
	for _, match := range matches {
		snapshots = append(snapshots, strings.TrimPrefix(filepath.Base(match), snapshotPrefix))
	}
	return snapshots, nil
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

// createFileAndWrite creates a file with the given path and
// writes the given contents
func createFileAndWrite(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return nil
}

// addNetworkFlags adds the flags in [networkFlags] to [nodeConfig.Flags].
// [nodeFlags] must not be nil.
func addNetworkFlags(log logging.Logger, networkFlags map[string]interface{}, nodeFlags map[string]interface{}) {
	for flagName, flagVal := range networkFlags {
		// If the same flag is given in network config and node config,
		// the flag in the node config takes precedence
		if val, ok := nodeFlags[flagName]; !ok {
			nodeFlags[flagName] = flagVal
		} else {
			log.Info(
				"not overwriting node config flag %s (value %v) with network config flag (value %v)",
				flagName, val, flagVal,
			)
		}
	}
}

// Set [nodeConfig].Name if it isn't given and assert it's unique.
func (ln *localNetwork) setNodeName(nodeConfig *node.Config) error {
	// If no name was given, use default name pattern
	if len(nodeConfig.Name) == 0 {
		for {
			nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, ln.nextNodeSuffix)
			ln.nextNodeSuffix++
			_, ok := ln.nodes[nodeConfig.Name]
			if !ok {
				break
			}
		}
	}
	// Enforce name uniqueness
	if _, ok := ln.nodes[nodeConfig.Name]; ok {
		return fmt.Errorf("repeated node name %q", nodeConfig.Name)
	}
	return nil
}

func makeNodeDir(log logging.Logger, rootDir, nodeName string) (string, error) {
	if rootDir == "" {
		log.Warn("no network root directory defined; will create this node's runtime directory in working directory")
	}
	// [nodeRootDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Profiles?
	nodeRootDir := filepath.Join(rootDir, nodeName)
	if err := os.Mkdir(nodeRootDir, 0o755); err != nil {
		if os.IsExist(err) {
			log.Warn("node root directory %s already exists", nodeRootDir)
		} else {
			return "", fmt.Errorf("error creating temp dir: %w", err)
		}
	}
	return nodeRootDir, nil
}

// getConfigEntry returns an entry in the config file if it is found, otherwise returns the default value
func getConfigEntry(
	nodeConfigFlags map[string]interface{},
	configFile map[string]interface{},
	flag string,
	defaultVal string,
) (string, error) {
	var entry string
	if val, ok := nodeConfigFlags[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected node config flag %q to be string but got %T", flag, entry)
	}
	if val, ok := configFile[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected config file flag %q to be string but got %T", flag, entry)
	}
	return defaultVal, nil
}

// getPort looks up the port config in the config file, if there is none, it tries to get a random free port from the OS
func getPort(
	flags map[string]interface{},
	configFile map[string]interface{},
	portKey string,
) (port uint16, err error) {
	if portIntf, ok := flags[portKey]; ok {
		if portFromFlags, ok := portIntf.(int); ok {
			port = uint16(portFromFlags)
		} else if portFromFlags, ok := portIntf.(float64); ok {
			port = uint16(portFromFlags)
		} else {
			return 0, fmt.Errorf("expected flag %q to be int/float64 but got %T", portKey, portIntf)
		}
	} else if portIntf, ok := configFile[portKey]; ok {
		if portFromConfigFile, ok := portIntf.(float64); ok {
			port = uint16(portFromConfigFile)
		} else {
			return 0, fmt.Errorf("expected flag %q to be float64 but got %T", portKey, portIntf)
		}
	} else {
		// Use a random free port.
		// Note: it is possible but unlikely for getFreePort to return the same port multiple times.
		port, err = getFreePort()
		if err != nil {
			return 0, fmt.Errorf("couldn't get free API port: %w", err)
		}
	}
	return port, nil
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
) ([]string, uint16, uint16, string, string, error) {
	// Add flags in [ln.Flags] to [nodeConfig.Flags]
	// Assumes [nodeConfig.Flags] is non-nil
	addNetworkFlags(ln.log, ln.flags, nodeConfig.Flags)

	// Tell the node to put the database in [nodeDir] unless given in config file
	dbDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.DBPathKey, filepath.Join(nodeDir, defaultDbSubdir))
	if err != nil {
		return nil, 0, 0, "", "", err
	}

	// Tell the node to put the log directory in [nodeDir/logs] unless given in config file
	logsDir, err := getConfigEntry(nodeConfig.Flags, configFile, config.LogsDirKey, filepath.Join(nodeDir, defaultLogsSubdir))
	if err != nil {
		return nil, 0, 0, "", "", err
	}

	// Use random free API port unless given in config file
	apiPort, err := getPort(nodeConfig.Flags, configFile, config.HTTPPortKey)
	if err != nil {
		return nil, 0, 0, "", "", err
	}

	// Use a random free P2P (staking) port unless given in config file
	// Use random free API port unless given in config file
	p2pPort, err := getPort(nodeConfig.Flags, configFile, config.StakingPortKey)
	if err != nil {
		return nil, 0, 0, "", "", err
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
		return nil, 0, 0, "", "", err
	}
	flags = append(flags, fileFlags...)

	// Add flags given in node config.
	// Note these will overwrite existing flags if the same flag is given twice.
	for flagName, flagVal := range nodeConfig.Flags {
		if _, ok := warnFlags[flagName]; ok {
			ln.log.Warn("The flag %s has been provided. This can create conflicts with the runner. The suggestion is to remove this flag", flagName)
		}
		flags = append(flags, fmt.Sprintf("--%s=%v", flagName, flagVal))
	}

	ln.log.Info(
		"adding node %q with tmp dir at %s, logs at %s, DB at %s, P2P port %d, API port %d",
		nodeConfig.Name, nodeDir, logsDir, dbDir, p2pPort, apiPort,
	)
	return flags, apiPort, p2pPort, dbDir, logsDir, nil
}

// writeFiles writes the files a node needs on startup.
// It returns flags used to point to those files.
func writeFiles(genesis []byte, nodeRootDir string, nodeConfig *node.Config) ([]string, error) {
	type file struct {
		pathKey   string
		flagValue string
		path      string
		contents  []byte
	}
	files := []file{
		{
			flagValue: filepath.Join(nodeRootDir, stakingKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingKeyFileName),
			pathKey:   config.StakingKeyPathKey,
			contents:  []byte(nodeConfig.StakingKey),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingCertFileName),
			path:      filepath.Join(nodeRootDir, stakingCertFileName),
			pathKey:   config.StakingCertPathKey,
			contents:  []byte(nodeConfig.StakingCert),
		},
		{
			flagValue: filepath.Join(nodeRootDir, genesisFileName),
			path:      filepath.Join(nodeRootDir, genesisFileName),
			pathKey:   config.GenesisConfigFileKey,
			contents:  genesis,
		},
	}
	if len(nodeConfig.ConfigFile) != 0 {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, configFileName),
			path:      filepath.Join(nodeRootDir, configFileName),
			pathKey:   config.ConfigFileKey,
			contents:  []byte(nodeConfig.ConfigFile),
		})
	}
	flags := []string{}
	for _, f := range files {
		flags = append(flags, fmt.Sprintf("--%s=%s", f.pathKey, f.flagValue))
		if err := createFileAndWrite(f.path, f.contents); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", f.path, err)
		}
	}
	if nodeConfig.ChainConfigFiles != nil {
		// only one flag and multiple files
		chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ChainConfigDirKey, chainConfigDir))
		for chainAlias, chainConfigFile := range nodeConfig.ChainConfigFiles {
			chainConfigPath := filepath.Join(chainConfigDir, chainAlias, configFileName)
			if err := createFileAndWrite(chainConfigPath, []byte(chainConfigFile)); err != nil {
				return nil, fmt.Errorf("couldn't write file at %q: %w", chainConfigPath, err)
			}
		}
	}
	return flags, nil
}
