package local

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/hashing"
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
	healthCheckFreq       = 5 * time.Second
	healthyTimeout        = 100 * time.Second
)

var errStopped = errors.New("network stopped")

// interface compliance
var _ network.Network = (*localNetwork)(nil)

// network keeps information uses for network management, and accessing all the nodes
type localNetwork struct {
	lock sync.RWMutex
	log  logging.Logger
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	// Node type --> Path to binary
	nodeTypeToBinaryPath map[NodeType]string
	// For node name generation
	nextNodeSuffix uint64
	// Node Name --> Node
	nodes map[string]*localNode
	// List of nodes that new nodes will bootstrap from.
	bootstrapIPs, bootstrapIDs beaconList
}

type beaconList []string

func (l beaconList) String() string {
	if len(l) == 0 {
		return ""
	}
	s := strings.Builder{}
	for i := 0; i < len(l); i++ {
		if i != 0 {
			_, _ = s.WriteString(",")
		}
		_, _ = s.WriteString(l[i])
	}
	return s.String()
}

// NewNetwork creates a network from given configuration and map of node kinds to binaries
func NewNetwork(
	log logging.Logger,
	networkConfig network.Config,
	nodeTypeToBinaryPath map[NodeType]string,
) (network.Network, error) {
	if err := networkConfig.Validate(); err != nil {
		return nil, fmt.Errorf("config failed validation: %w", err)
	}
	log.Info("creating network with %d nodes", len(networkConfig.NodeConfigs))
	// Create the network
	net := &localNetwork{
		nodes:                map[string]*localNode{},
		closedOnStopCh:       make(chan struct{}),
		nodeTypeToBinaryPath: nodeTypeToBinaryPath,
		log:                  log,
	}

	for _, nodeConfig := range networkConfig.NodeConfigs {
		if _, err := net.addNode(nodeConfig); err != nil {
			if err := net.stop(); err != nil {
				// Clean up nodes already created
				log.Warn("error while stopping network: %s", err)
			}
			return nil, fmt.Errorf("errored on adding node %s: %s", nodeConfig.Name, err)
		}
	}
	return net, nil
}

// See network.Network
func (ln *localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	return ln.addNode(nodeConfig)
}

// Assumes [ln.lock] is held.
// TODO make this method shorter
func (ln *localNetwork) addNode(nodeConfig node.Config) (node.Node, error) {
	if ln.isStopped() {
		return nil, errStopped
	}

	// If no name was given, use default name pattern
	if len(nodeConfig.Name) == 0 {
		nodeConfig.Name = fmt.Sprintf("%s%d", defaultNodeNamePrefix, ln.nextNodeSuffix)
		ln.nextNodeSuffix++
	}

	// Enforce name uniqueness
	if _, ok := ln.nodes[nodeConfig.Name]; ok {
		return nil, fmt.Errorf("repeated node name %s", nodeConfig.Name)
	}

	// If no staking key/cert is given, create them.
	// Note that len(nodeConfig.StakingKey) == 0 implies len(nodeConfig.StakingKey) == 0.
	if len(nodeConfig.StakingKey) == 0 {
		var err error
		nodeConfig.StakingCert, nodeConfig.StakingKey, err = staking.NewCertAndKeyBytes()
		if err != nil {
			return nil, fmt.Errorf("error creating staking key/cert: %w", err)
		}
	}

	// Get a free port to use as the P2P port
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("could not get free port: %w", err)
	}
	p2pPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// Get a free port for the API port
	l, err = net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("could not get free port: %w", err)
	}
	apiPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	// If this node is a beacon, add its IP/ID to the beacon lists
	if nodeConfig.IsBeacon {
		// Parse the node ID
		// TODO add helper in AvalancheGo for this?
		cert, err := tls.X509KeyPair(nodeConfig.StakingCert, nodeConfig.StakingKey)
		if err != nil {
			return nil, err
		}
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, err
		}
		nodeID := ids.ShortID(
			hashing.ComputeHash160Array(
				hashing.ComputeHash256(cert.Leaf.Raw),
			),
		)
		ln.bootstrapIDs = append(ln.bootstrapIDs, nodeID.PrefixedString(constants.NodeIDPrefix))
		ln.bootstrapIPs = append(ln.bootstrapIPs, fmt.Sprintf("127.0.0.1:%d", p2pPort))
	}

	// [tmpDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Profiles?
	tmpDir, err := os.MkdirTemp("", "avalanchego-network-runner-*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp dir: %w", err)
	}

	// Flags for AvalancheGo that point to the files
	// we're about to create.
	flags := []string{
		// Tell the node to put the database in [tmpDir]
		fmt.Sprintf("--%s=%s", config.DBPathKey, tmpDir),
		// Tell the node to put the log directory in [tmpDir]
		fmt.Sprintf("--%s=%s", config.LogsDirKey, filepath.Join(tmpDir, "logs")),
		// Tell the node to use this API port
		fmt.Sprintf("--%s=%d", config.HTTPPortKey, apiPort),
		// Tell the node to use this P2P (staking) port
		fmt.Sprintf("--%s=%d", config.StakingPortKey, p2pPort),
		// Tell the node which nodes to bootstrap from
		fmt.Sprintf("--%s=%s", config.BootstrapIPsKey, ln.bootstrapIPs),
		fmt.Sprintf("--%s=%s", config.BootstrapIDsKey, ln.bootstrapIDs),
	}

	ln.log.Info("adding node %q with files at %s. P2P port %d. API port %d", nodeConfig.Name, tmpDir, p2pPort, apiPort)

	// Write this node's staking key/cert.
	stakingKeyFilePath := filepath.Join(tmpDir, stakingKeyFileName)
	if err := createFileAndWrite(stakingKeyFilePath, nodeConfig.StakingKey); err != nil {
		return nil, fmt.Errorf("error writing staking key: %w", err)
	}
	flags = append(flags, fmt.Sprintf("--%s=%s", config.StakingKeyPathKey, stakingKeyFilePath))
	stakingCertFilePath := filepath.Join(tmpDir, stakingCertFileName)
	if err := createFileAndWrite(stakingCertFilePath, nodeConfig.StakingCert); err != nil {
		return nil, fmt.Errorf("error writing staking cert: %w", err)
	}
	flags = append(flags, fmt.Sprintf("--%s=%s", config.StakingCertPathKey, stakingCertFilePath))

	// Write this node's config file if one is given
	configFilePath := filepath.Join(tmpDir, configFileName)
	if len(nodeConfig.ConfigFile) != 0 {
		if err := createFileAndWrite(configFilePath, nodeConfig.ConfigFile); err != nil {
			return nil, fmt.Errorf("error creating/writing config file: %w", err)
		}
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ConfigFileKey, configFilePath))
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
	avalancheGoBinaryPath, ok := ln.nodeTypeToBinaryPath[nodeType]
	if !ok {
		return nil, fmt.Errorf("got unexpected node type %v", nodeType)
	}

	// Start the AvalancheGo node and pass it the flags defined above
	cmd := exec.Command(avalancheGoBinaryPath, flags...)
	// Optionally re-direct stdout and stderr
	if nodeConfig.Stdout != nil {
		cmd.Stdout = nodeConfig.Stdout
	}
	if nodeConfig.Stderr != nil {
		cmd.Stderr = nodeConfig.Stderr
	}
	ln.log.Info("starting node %q with \"%s %s\"", nodeConfig.Name, avalancheGoBinaryPath, flags) // TODO lower log levelq
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
	ln.nodes[node.name] = node
	return node, nil
}

// See network.Network
func (net *localNetwork) Healthy() chan error {
	net.lock.RLock()
	defer net.lock.RUnlock()

	healthyChan := make(chan error, 1)

	// Return unhealthy if the network is stopped
	if net.isStopped() {
		healthyChan <- errStopped
		return healthyChan
	}

	nodes := make([]*localNode, 0, len(net.nodes))
	for _, node := range net.nodes {
		nodes = append(nodes, node)
	}
	go func() {
		errGr, ctx := errgroup.WithContext(context.Background())
		for _, node := range nodes {
			node := node
			errGr.Go(func() error {
				// Every 5 seconds, query node for health status.
				// Do this up to 20 times.
				for i := 0; i < int(healthyTimeout/healthCheckFreq); i++ {
					select {
					case <-net.closedOnStopCh:
						return errStopped
					case <-ctx.Done():
						return nil
					case <-time.After(healthCheckFreq):
					}
					health, err := node.client.HealthAPI().Health()
					if err == nil && health.Healthy {
						net.log.Info("node %q became healthy", node.name)
						return nil
					}
				}
				return fmt.Errorf("node %q timed out on becoming healthy", node.name)
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
func (net *localNetwork) GetNode(nodeName string) (node.Node, error) {
	net.lock.RLock()
	defer net.lock.RUnlock()

	if net.isStopped() {
		return nil, errStopped
	}

	node, ok := net.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node %q not found in network", nodeName)
	}
	return node, nil
}

// See network.Network
func (net *localNetwork) GetNodesNames() []string {
	net.lock.RLock()
	defer net.lock.RUnlock()

	if net.isStopped() {
		return nil
	}

	names := make([]string, len(net.nodes))
	i := 0
	for name := range net.nodes {
		names[i] = name
		i++
	}
	return names
}

// TODO does this need to return an error?
func (net *localNetwork) Stop() error {
	net.lock.Lock()
	defer net.lock.Unlock()

	return net.stop()
}

// Assumes [net.lock] is held
func (net *localNetwork) stop() error {
	if net.isStopped() {
		net.log.Debug("stop() called multiple times")
		return nil
	}
	net.log.Info("stopping network")
	for nodeName := range net.nodes {
		if err := net.removeNode(nodeName); err != nil {
			net.log.Warn("error removing node %q: %s", nodeName, err)
		}
	}
	close(net.closedOnStopCh)
	net.log.Info("done stopping network") // todo remove / lower level
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
	if net.isStopped() {
		net.log.Debug("can't remove node %q because network stopped", nodeName)
	}
	net.log.Debug("removing node %q", nodeName)
	node, ok := net.nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found", nodeName)
	}
	delete(net.nodes, nodeName)
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

// Assumes [net.lock] is held
func (net *localNetwork) isStopped() bool {
	select {
	case <-net.closedOnStopCh:
		return true
	default:
		return false
	}
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
