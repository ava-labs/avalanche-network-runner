package local

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/staking"
	avago_utils "github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/beacon"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	defaultNodeNamePrefix = "node-"
	configFileName        = "config.json"
	stakingKeyFileName    = "staking.key"
	stakingCertFileName   = "staking.crt"
	genesisFileName       = "genesis.json"
	stopTimeout           = 30 * time.Second
	healthCheckFreq       = 3 * time.Second
	DefaultNumNodes       = 5
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
	cChainConfigSubDir = filepath.Join(chainConfigSubDir, "C")
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
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
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
	//
	unexpectedNodeStop chan struct{}
}

var (
	//go:embed default
	embeddedDefaultNetworkConfigDir embed.FS
	// Pre-defined network configuration. The ImplSpecificConfig
	// field of each node in [defaultNetworkConfig.NodeConfigs]
	// is not defined.
	// [defaultNetworkConfig] should not be modified.
	// TODO add method Copy() to network.Config to prevent
	// accidental overwriting
	defaultNetworkConfig network.Config
)

// populate default network config from embedded default directory
func init() {
	configsDir, err := fs.Sub(embeddedDefaultNetworkConfigDir, "default")
	if err != nil {
		panic(err)
	}

	defaultNetworkConfig = network.Config{
		Name:        "my network",
		NodeConfigs: make([]node.Config, DefaultNumNodes),
		LogLevel:    "INFO",
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
		defaultNetworkConfig.NodeConfigs[i].CChainConfigFile = string(cChainConfig)
		defaultNetworkConfig.NodeConfigs[i].IsBeacon = true
	}
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
			return nil, fmt.Errorf("Could not create stdout pipe: %s", err)
		}
		// redirect stdout and assign a color to the text
		utils.ColorAndPrepend(stdout, npc.stdout, config.Name, color)
	}
	if config.RedirectStderr {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("Could not create stderr pipe: %s", err)
		}
		// redirect stderr and assign a color to the text
		utils.ColorAndPrepend(stderr, npc.stderr, config.Name, color)
	}
	return &nodeProcessImpl{cmd: cmd}, nil
}

// NewNetwork returns a new network from the given config that uses the given log.
// Files (e.g. logs, databases) default to being written at directory [dir].
// If there isn't a directory at [dir] one will be created.
// If len([dir]) == 0, files will be written underneath a new temporary directory.
func NewNetwork(
	log logging.Logger,
	networkConfig network.Config,
	dir string,
) (network.Network, error) {
	return newNetwork(
		log,
		networkConfig,
		api.NewAPIClient,
		&nodeProcessCreator{
			colorPicker: utils.NewColorPicker(),
			stdout:      os.Stdout,
			stderr:      os.Stderr,
		},
		dir,
	)
}

// See NewNetwork.
// [newAPIClientF] is used to create new API clients.
// [nodeProcessCreator] is used to launch new avalanchego processes.
func newNetwork(
	log logging.Logger,
	networkConfig network.Config,
	newAPIClientF api.NewAPIClientF,
	nodeProcessCreator NodeProcessCreator,
	dir string,
) (network.Network, error) {
	if err := networkConfig.Validate(); err != nil {
		return nil, fmt.Errorf("config failed validation: %w", err)
	}
	log.Info("creating network with %d nodes", len(networkConfig.NodeConfigs))

	networkID, err := utils.NetworkIDFromGenesis([]byte(networkConfig.Genesis))
	if err != nil {
		return nil, fmt.Errorf("couldn't get network ID from genesis: %w", err)
	}

	// Create the network
	net := &localNetwork{
		networkID:          networkID,
		genesis:            []byte(networkConfig.Genesis),
		nodes:              map[string]*localNode{},
		closedOnStopCh:     make(chan struct{}),
		log:                log,
		bootstraps:         beacon.NewSet(),
		newAPIClientF:      newAPIClientF,
		nodeProcessCreator: nodeProcessCreator,
		flags:              networkConfig.Flags,
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

	if dir == "" {
		net.rootDir, err = os.MkdirTemp("", "avalanche-network-runner-*")
		if err != nil {
			return nil, err
		}
	} else {
		net.rootDir = dir
	}

	for _, nodeConfig := range nodeConfigs {
		if _, err := net.addNode(nodeConfig); err != nil {
			if err := net.stop(context.Background()); err != nil {
				// Clean up nodes already created
				log.Debug("error stopping network: %s", err)
			}
			return nil, fmt.Errorf("error adding node %s: %s", nodeConfig.Name, err)
		}
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
	return newDefaultNetwork(log, binaryPath, api.NewAPIClient, &nodeProcessCreator{
		colorPicker: utils.NewColorPicker(),
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	})
}

func newDefaultNetwork(
	log logging.Logger,
	binaryPath string,
	newAPIClientF api.NewAPIClientF,
	nodeProcessCreator NodeProcessCreator,
) (network.Network, error) {
	config := NewDefaultConfig(binaryPath)
	return newNetwork(log, config, newAPIClientF, nodeProcessCreator, "")
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

// See network.Network
func (ln *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	return ln.addNode(nodeConfig)
}

// Assumes [ln.lock] is held.
func (ln *localNetwork) addNode(nodeConfig node.Config) (node.Node, error) {
	if ln.isStopped() {
		return nil, network.ErrStopped
	}

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

	flags, apiPort, p2pPort, err := ln.buildFlags(configFile, nodeDir, &nodeConfig)
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
	if err := nodeProcess.Start(ln.unexpectedNodeStop); err != nil {
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
	}
	ln.nodes[node.name] = node
	// If this node is a beacon, add its IP/ID to the beacon lists.
	// Note that we do this *after* we set this node's bootstrap IPs/IDs
	// so this node won't try to use itself as a beacon.
	if nodeConfig.IsBeacon {
		err = ln.bootstraps.Add(beacon.New(nodeID, avago_utils.IPDesc{
			IP:   net.IPv6loopback,
			Port: p2pPort,
		}))
	}
	return node, err
}

// See network.Network
func (ln *localNetwork) Healthy(ctx context.Context) chan error {
	zap.L().Info("checking local network healthiness", zap.Int("nodes", len(ln.nodes)))
	healthyChan := make(chan error, 1)

	// Return unhealthy if the network is stopped
	if ln.isStopped() {
		healthyChan <- network.ErrStopped
		return healthyChan
	}

	go func() {
		// TODO: This will block the network for the duration of the health call.
		// Maybe a better solution can be found.
		ln.lock.RLock()
		defer ln.lock.RUnlock()

		errGr, cctx := errgroup.WithContext(ctx)
		for _, node := range ln.nodes {
			node := node
			errGr.Go(func() error {
				// Every constants.HealthCheckInterval, query node for health status.
				// Do this until ctx timeout
				for {
					select {
					case <-ln.unexpectedNodeStop:
						return network.ErrUnexpectedNodeStop
					case <-ln.closedOnStopCh:
						return network.ErrStopped
					case <-cctx.Done():
						return fmt.Errorf("node %q failed to become healthy within timeout", node.GetName())
					case <-time.After(healthCheckFreq):
					}
					health, err := node.client.HealthAPI().Health(cctx)
					if err == nil && health.Healthy {
						ln.log.Debug("node %q became healthy", node.name)
						return nil
					}
				}
			})
		}
		// Wait until all nodes are ready or timeout
		if err := errGr.Wait(); err != nil {
			healthyChan <- err
		}
		close(healthyChan)
	}()
	return healthyChan
}

// See network.Network
func (ln *localNetwork) GetNode(nodeName string) (node.Node, error) {
	ln.lock.RLock()
	defer ln.lock.RUnlock()

	if ln.isStopped() {
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

	if ln.isStopped() {
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

	if ln.isStopped() {
		return nil, network.ErrStopped
	}

	nodesCopy := make(map[string]node.Node, len(ln.nodes))
	for name, node := range ln.nodes {
		nodesCopy[name] = node
	}
	return nodesCopy, nil
}

func (ln *localNetwork) Stop(ctx context.Context) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	return ln.stop(ctx)
}

// Assumes [net.lock] is held
func (ln *localNetwork) stop(ctx context.Context) error {
	if ln.isStopped() {
		ln.log.Debug("stop() called multiple times")
		return network.ErrStopped
	}
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
	close(ln.closedOnStopCh)
	ln.log.Info("done stopping network")
	return errs.Err
}

// Sends a SIGTERM to the given node and removes it from this network
func (ln *localNetwork) RemoveNode(nodeName string) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	return ln.removeNode(nodeName)
}

// Assumes [net.lock] is held
func (ln *localNetwork) removeNode(nodeName string) error {
	if ln.isStopped() {
		return network.ErrStopped
	}
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

// Assumes [net.lock] is held
func (ln *localNetwork) isStopped() bool {
	select {
	case <-ln.closedOnStopCh:
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
		nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, ln.nextNodeSuffix)
		ln.nextNodeSuffix++
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
func getConfigEntry(configFile map[string]interface{}, flag string, defaultVal string) (string, error) {
	var entry string
	if val, ok := configFile[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected flag %q to be string but got %T", flag, entry)
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
		} else {
			return 0, fmt.Errorf("expected flag %q to be int but got %T", portKey, portIntf)
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
) ([]string, uint16, uint16, error) {
	// Add flags in [ln.Flags] to [nodeConfig.Flags]
	// Assumes [nodeConfig.Flags] is non-nil
	addNetworkFlags(ln.log, ln.flags, nodeConfig.Flags)

	// Tell the node to put the database in [nodeDir] unless given in config file
	dbPath, err := getConfigEntry(configFile, config.DBPathKey, nodeDir)
	if err != nil {
		return nil, 0, 0, err
	}

	// Tell the node to put the log directory in [nodeDir/logs] unless given in config file
	logsDir, err := getConfigEntry(configFile, config.LogsDirKey, filepath.Join(nodeDir, "logs"))
	if err != nil {
		return nil, 0, 0, err
	}

	// Use random free API port unless given in config file
	apiPort, err := getPort(nodeConfig.Flags, configFile, config.HTTPPortKey)
	if err != nil {
		return nil, 0, 0, err
	}

	// Use a random free P2P (staking) port unless given in config file
	// Use random free API port unless given in config file
	p2pPort, err := getPort(nodeConfig.Flags, configFile, config.StakingPortKey)
	if err != nil {
		return nil, 0, 0, err
	}

	// Flags for AvalancheGo
	flags := []string{
		fmt.Sprintf("--%s=%d", config.NetworkNameKey, ln.networkID),
		fmt.Sprintf("--%s=%s", config.DBPathKey, dbPath),
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
		return nil, 0, 0, err
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
		nodeConfig.Name, nodeDir, logsDir, dbPath, p2pPort, apiPort,
	)
	return flags, apiPort, p2pPort, nil
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
	if len(nodeConfig.CChainConfigFile) != 0 {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, chainConfigSubDir),
			path:      filepath.Join(nodeRootDir, cChainConfigSubDir, configFileName),
			pathKey:   config.ChainConfigDirKey,
			contents:  []byte(nodeConfig.CChainConfigFile),
		})
	}
	flags := []string{}
	for _, f := range files {
		flags = append(flags, fmt.Sprintf("--%s=%s", f.pathKey, f.flagValue))
		if err := createFileAndWrite(f.path, f.contents); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", f.path, err)
		}
	}
	return flags, nil
}
