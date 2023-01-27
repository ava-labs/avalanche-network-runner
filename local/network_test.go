package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	apimocks "github.com/ava-labs/avalanche-network-runner/api/mocks"
	"github.com/ava-labs/avalanche-network-runner/local/mocks"
	healthmocks "github.com/ava-labs/avalanche-network-runner/local/mocks/health"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	defaultHealthyTimeout = 5 * time.Second
	nodeVersion           = "avalanche/1.9.5 extra"
)

var (
	_ NodeProcessCreator    = &localTestSuccessfulNodeProcessCreator{}
	_ NodeProcessCreator    = &localTestFailedStartProcessCreator{}
	_ NodeProcessCreator    = &localTestProcessUndefNodeProcessCreator{}
	_ NodeProcessCreator    = &localTestFlagCheckProcessCreator{}
	_ api.NewAPIClientF     = newMockAPISuccessful
	_ api.NewAPIClientF     = newMockAPIUnhealthy
	_ router.InboundHandler = &noOpInboundHandler{}
)

type localTestSuccessfulNodeProcessCreator struct{}

func (*localTestSuccessfulNodeProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	return newMockProcessSuccessful(config, flags...)
}

func (*localTestSuccessfulNodeProcessCreator) GetNodeVersion(_ node.Config) (string, error) {
	return nodeVersion, nil
}

type localTestFailedStartProcessCreator struct{}

func (*localTestFailedStartProcessCreator) NewNodeProcess(node.Config, ...string) (NodeProcess, error) {
	return nil, errors.New("error on purpose for test")
}

func (*localTestFailedStartProcessCreator) GetNodeVersion(_ node.Config) (string, error) {
	return nodeVersion, nil
}

type localTestProcessUndefNodeProcessCreator struct{}

func (*localTestProcessUndefNodeProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	return newMockProcessUndef(config, flags...)
}

func (*localTestProcessUndefNodeProcessCreator) GetNodeVersion(_ node.Config) (string, error) {
	return nodeVersion, nil
}

type localTestFlagCheckProcessCreator struct {
	expectedFlags map[string]interface{}
	require       *require.Assertions
}

func (lt *localTestFlagCheckProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	lt.require.EqualValues(lt.expectedFlags, config.Flags)
	return newMockProcessSuccessful(config, flags...)
}

func (*localTestFlagCheckProcessCreator) GetNodeVersion(_ node.Config) (string, error) {
	return nodeVersion, nil
}

// Returns an API client where:
// * The Health API's Health method always returns healthy
// * The CChainEthAPI's Close method may be called
// * Only the above 2 methods may be called
// TODO have this method return an API Client that has all
// APIs and methods implemented
func newMockAPISuccessful(string, uint16) api.Client {
	healthReply := &health.APIReply{Healthy: true}
	healthClient := &healthmocks.Client{}
	healthClient.On("Health", mock.Anything).Return(healthReply, nil)
	// ethClient used when removing nodes, to close websocket connection
	ethClient := &apimocks.EthClient{}
	ethClient.On("Close").Return()
	client := &apimocks.Client{}
	client.On("HealthAPI").Return(healthClient)
	client.On("CChainEthAPI").Return(ethClient)
	return client
}

// Returns an API client where the Health API's Health method always returns unhealthy
func newMockAPIUnhealthy(string, uint16) api.Client {
	healthReply := &health.APIReply{Healthy: false}
	healthClient := &healthmocks.Client{}
	healthClient.On("Health", mock.Anything).Return(healthReply, nil)
	client := &apimocks.Client{}
	client.On("HealthAPI").Return(healthClient)
	return client
}

func newMockProcessUndef(node.Config, ...string) (NodeProcess, error) {
	return &mocks.NodeProcess{}, nil
}

// Returns a NodeProcess that always returns nil
func newMockProcessSuccessful(node.Config, ...string) (NodeProcess, error) {
	process := &mocks.NodeProcess{}
	process.On("Wait").Return(nil)
	process.On("Stop", mock.Anything).Return(0)
	process.On("Status").Return(status.Running)
	return process, nil
}

type noOpInboundHandler struct{}

func (*noOpInboundHandler) HandleInbound(context.Context, message.InboundMessage) {}

// Start a network with no nodes
func TestNewNetworkEmpty(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	networkConfig.NodeConfigs = nil
	net, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestProcessUndefNodeProcessCreator{},
		"",
		"",
		false,
	)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	// Assert that GetNodeNames() returns an empty list
	names, err := net.GetNodeNames()
	require.NoError(err)
	require.Len(names, 0)
}

type localTestOneNodeCreator struct {
	require        *require.Assertions
	networkConfig  network.Config
	successCreator *localTestSuccessfulNodeProcessCreator
}

func newLocalTestOneNodeCreator(require *require.Assertions, networkConfig network.Config) *localTestOneNodeCreator {
	return &localTestOneNodeCreator{
		require:        require,
		networkConfig:  networkConfig,
		successCreator: &localTestSuccessfulNodeProcessCreator{},
	}
}

// Assert that the node's config is being passed correctly
// to the function that starts the node process.
func (lt *localTestOneNodeCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	lt.require.True(config.IsBeacon)
	expectedConfig := lt.networkConfig.NodeConfigs[0]
	lt.require.EqualValues(lt.networkConfig.ChainConfigFiles, config.ChainConfigFiles)
	lt.require.EqualValues(expectedConfig.ConfigFile, config.ConfigFile)
	lt.require.EqualValues(lt.networkConfig.BinaryPath, config.BinaryPath)
	lt.require.EqualValues(expectedConfig.IsBeacon, config.IsBeacon)
	lt.require.EqualValues(expectedConfig.Name, config.Name)
	lt.require.EqualValues(expectedConfig.StakingCert, config.StakingCert)
	lt.require.EqualValues(expectedConfig.StakingKey, config.StakingKey)
	lt.require.Len(config.Flags, len(expectedConfig.Flags))
	for k, v := range expectedConfig.Flags {
		gotV, ok := config.Flags[k]
		lt.require.True(ok)
		lt.require.EqualValues(v, gotV)
	}
	return lt.successCreator.NewNodeProcess(config, flags...)
}

func (*localTestOneNodeCreator) GetNodeVersion(_ node.Config) (string, error) {
	return nodeVersion, nil
}

// Start a network with one node.
func TestNewNetworkOneNode(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	networkConfig.NodeConfigs = networkConfig.NodeConfigs[:1]
	creator := newLocalTestOneNodeCreator(require, networkConfig)
	net, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		creator,
		"",
		"",
		false,
	)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)

	// Assert that GetNodeNames() includes only the 1 node's name
	names, err := net.GetNodeNames()
	require.NoError(err)
	require.Contains(names, networkConfig.NodeConfigs[0].Name)
	require.Len(names, 1)

	// Assert that the network's genesis was set
	require.EqualValues(networkConfig.Genesis, net.genesis)
}

// Test that NewNetwork returns an error when
// starting a node returns an error
func TestNewNetworkFailToStartNode(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestFailedStartProcessCreator{},
		"",
		"",
		false,
	)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.Error(err)
}

// Check configs that are expected to be invalid at network creation time
func TestWrongNetworkConfigs(t *testing.T) {
	t.Parallel()
	refNetworkConfig := testNetworkConfig(t)
	tests := map[string]struct {
		config network.Config
	}{
		"config file unmarshal": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "nonempty",
					},
				},
			},
		},
		"wrong network id type in config file": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"network-id\": \"0\"}",
					},
				},
			},
		},
		"wrong db dir type in config": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"db-dir\": 0}",
					},
				},
			},
		},
		"wrong log dir type in config": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"log-dir\": 0}",
					},
				},
			},
		},
		"wrong http port type in config": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"http-port\": \"0\"}",
					},
				},
			},
		},
		"wrong staking port type in config": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"staking-port\": \"0\"}",
					},
				},
			},
		},
		"network id mismatch": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
						ConfigFile:  "{\"network-id\": 1}",
					},
				},
			},
		},
		"genesis unmarshall": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"no network id in genesis": {
			config: network.Config{
				Genesis: "{}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"wrong network id type in genesis": {
			config: network.Config{
				Genesis: "{\"networkID\": \"0\"}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"no Genesis": {
			config: network.Config{
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"StakingKey but no StakingCert": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath: "pepe",
						IsBeacon:   true,
						StakingKey: refNetworkConfig.NodeConfigs[0].StakingKey,
					},
				},
			},
		},
		"StakingCert but no StakingKey": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"invalid staking cert/key": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  "nonempty",
						StakingCert: "nonempty",
					},
				},
			},
		},
		"no beacon node": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						BinaryPath:  "pepe",
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
				},
			},
		},
		"repeated name": {
			config: network.Config{
				Genesis: "{\"networkID\": 0}",
				NodeConfigs: []node.Config{
					{
						Name:        "node1",
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[0].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[0].StakingCert,
					},
					{
						Name:        "node1",
						BinaryPath:  "pepe",
						IsBeacon:    true,
						StakingKey:  refNetworkConfig.NodeConfigs[1].StakingKey,
						StakingCert: refNetworkConfig.NodeConfigs[1].StakingCert,
					},
				},
			},
		},
	}
	require := require.New(t)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
			require.NoError(err)
			err = net.loadConfig(context.Background(), tt.config)
			require.Error(err)
		})
	}
}

// Assert that the network's Healthy() method returns an
// error when all nodes' Health API return unhealthy
func TestUnhealthyNetwork(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPIUnhealthy, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	require.Error(awaitNetworkHealthy(net, defaultHealthyTimeout))
}

// Create a network without giving names to nodes.
// Checks that the generated names are the correct number and unique.
func TestGeneratedNodesNames(t *testing.T) {
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	for i := range networkConfig.NodeConfigs {
		networkConfig.NodeConfigs[i].Name = ""
	}
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	nodeNameMap := make(map[string]bool)
	nodeNames, err := net.GetNodeNames()
	require.NoError(err)
	for _, nodeName := range nodeNames {
		nodeNameMap[nodeName] = true
	}
	require.EqualValues(len(nodeNameMap), len(networkConfig.NodeConfigs))
}

// TestGenerateDefaultNetwork create a default network with config from NewDefaultConfig and
// check expected number of nodes, node names, and avalanchego node ids
func TestGenerateDefaultNetwork(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	binaryPath := "pepito"
	networkConfig := NewDefaultConfig(binaryPath)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	require.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))
	names, err := net.GetNodeNames()
	require.NoError(err)
	require.Len(names, 5)
	for _, nodeInfo := range []struct {
		name string
		ID   string
	}{
		{
			"node1",
			"NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg",
		},
		{
			"node2",
			"NodeID-MFrZFVCXPv5iCn6M9K6XduxGTYp891xXZ",
		},
		{
			"node3",
			"NodeID-NFBbbJ4qCmNaCzeW7sxErhvWqvEQMnYcN",
		},
		{
			"node4",
			"NodeID-GWPcbFJZFfZreETSoWjPimr846mXEKCtu",
		},
		{
			"node5",
			"NodeID-P7oB2McjBGgW2NXXWVYjV8JEDFoW9xDE5",
		},
	} {
		require.Contains(names, nodeInfo.name)
		node, err := net.GetNode(nodeInfo.name)
		require.NoError(err)
		require.EqualValues(nodeInfo.name, node.GetName())
		expectedID, err := ids.NodeIDFromString(nodeInfo.ID)
		require.NoError(err)
		require.EqualValues(expectedID, node.GetNodeID())
	}
}

// TODO add byzantine node to conf
// TestNetworkFromConfig creates/waits/checks/stops a network from config file
// the check verify that all the nodes can be accessed
func TestNetworkFromConfig(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	require.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))
	runningNodes := make(map[string]struct{})
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = struct{}{}
	}
	checkNetwork(t, net, runningNodes, nil)
}

// TestNetworkNodeOps creates an empty network,
// adds nodes one by one, then removes nodes one by one.
// Setween all operations, a network check is performed
// to verify that all the running nodes are in the network,
// and all removed nodes are not.
func TestNetworkNodeOps(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	// Start a new, empty network
	emptyNetworkConfig, err := emptyNetworkConfig()
	require.NoError(err)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	require.NoError(err)
	runningNodes := make(map[string]struct{})

	// Add nodes to the network one by one
	networkConfig := testNetworkConfig(t)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.AddNode(nodeConfig)
		require.NoError(err)
		runningNodes[nodeConfig.Name] = struct{}{}
		checkNetwork(t, net, runningNodes, nil)
	}
	// Wait for all nodes to be healthy
	require.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))

	// Remove nodes one by one
	removedNodes := make(map[string]struct{})
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.GetNode(nodeConfig.Name)
		require.NoError(err)
		err = net.RemoveNode(context.Background(), nodeConfig.Name)
		require.NoError(err)
		removedNodes[nodeConfig.Name] = struct{}{}
		delete(runningNodes, nodeConfig.Name)
		checkNetwork(t, net, runningNodes, removedNodes)
	}
}

// TestNodeNotFound checks all operations fail for an unknown node,
// being it either not created, or created and removed thereafter
func TestNodeNotFound(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	require.NoError(err)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	require.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	require.NoError(err)
	// get node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	require.NoError(err)
	// get non-existent node
	_, err = net.GetNode(networkConfig.NodeConfigs[1].Name)
	require.Error(err)
	// remove non-existent node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[1].Name)
	require.Error(err)
	// remove node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	require.NoError(err)
	// get removed node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	require.Error(err)
	// remove already-removed node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	require.Error(err)
}

// TestStoppedNetwork checks that operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	require.NoError(err)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	require.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	require.NoError(err)
	// first GetNodeNames should return some nodes
	_, err = net.GetNodeNames()
	require.NoError(err)
	err = net.Stop(context.Background())
	require.NoError(err)
	// Stop failure
	require.EqualValues(net.Stop(context.Background()), network.ErrStopped)
	// AddNode failure
	_, err = net.AddNode(networkConfig.NodeConfigs[1])
	require.EqualValues(network.ErrStopped, err)
	// GetNode failure
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	require.EqualValues(err, network.ErrStopped)
	// second GetNodeNames should return no nodes
	_, err = net.GetNodeNames()
	require.EqualValues(network.ErrStopped, err)
	// RemoveNode failure
	require.EqualValues(network.ErrStopped, net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name))
	// Healthy failure
	require.EqualValues(awaitNetworkHealthy(net, defaultHealthyTimeout), network.ErrStopped)
	_, err = net.GetAllNodes()
	require.EqualValues(err, network.ErrStopped)
}

func TestGetAllNodes(t *testing.T) {
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)

	nodes, err := net.GetAllNodes()
	require.NoError(err)
	require.Len(nodes, len(net.nodes))
	for name, node := range net.nodes {
		require.EqualValues(node, nodes[name])
	}
}

// TestFlags tests that we can pass flags through the network.Config
// but also via node.Config and that the latter overrides the former
// if same keys exist.
func TestFlags(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	networkConfig := testNetworkConfig(t)

	// submit both network.Config flags and node.Config
	networkConfig.Flags = map[string]interface{}{
		"test-network-config-flag": "something",
		"common-config-flag":       "should not be added",
	}
	for i := range networkConfig.NodeConfigs {
		v := &networkConfig.NodeConfigs[i]
		v.Flags = map[string]interface{}{
			"test-node-config-flag":  "node",
			"test2-node-config-flag": "config",
			"common-config-flag":     "this should be added",
		}
	}
	expectedFlags := map[string]interface{}{
		"test-network-config-flag": "something",
		"common-config-flag":       "this should be added",
		"test-node-config-flag":    "node",
		"test2-node-config-flag":   "config",
	}

	nw, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestFlagCheckProcessCreator{
			// after creating the network, one flag should have been overridden by the node configs
			expectedFlags: expectedFlags,
			require:       require,
		},
		"",
		"",
		false,
	)
	require.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	err = nw.Stop(context.Background())
	require.NoError(err)

	// submit only node.Config flags
	networkConfig.Flags = nil
	flags := map[string]interface{}{
		"test-node-config-flag":  "node",
		"test2-node-config-flag": "config",
		"common-config-flag":     "this should be added",
	}
	for i := range networkConfig.NodeConfigs {
		v := &networkConfig.NodeConfigs[i]
		v.Flags = flags
	}
	nw, err = newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestFlagCheckProcessCreator{
			// after creating the network, only node configs should exist
			expectedFlags: flags,
			require:       require,
		},
		"",
		"",
		false,
	)
	require.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	err = nw.Stop(context.Background())
	require.NoError(err)

	// submit only network.Config flags
	flags = map[string]interface{}{
		"test-network-config-flag": "something",
		"common-config-flag":       "else",
	}
	networkConfig.Flags = flags
	for i := range networkConfig.NodeConfigs {
		v := &networkConfig.NodeConfigs[i]
		v.Flags = nil
	}
	nw, err = newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestFlagCheckProcessCreator{
			// after creating the network, only flags from the network config should exist
			expectedFlags: flags,
			require:       require,
		},
		"",
		"",
		false,
	)
	require.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	require.NoError(err)
	err = nw.Stop(context.Background())
	require.NoError(err)
}

// for the TestChildCmdRedirection we need to be able to wait
// until the buffer is written to or else there is a race condition
type lockedBuffer struct {
	bytes.Buffer
	// [writtenCh] is closed after Write is called
	writtenCh chan struct{}
}

// Write is locked for the lockedBuffer
func (m *lockedBuffer) Write(b []byte) (int, error) {
	defer close(m.writtenCh)
	return m.Buffer.Write(b)
}

// TestChildCmdRedirection checks that RedirectStdout set to true on a NodeConfig
// results indeed in the output being prepended and colored.
// For the color check we just measure the length of the required terminal escape values
func TestChildCmdRedirection(t *testing.T) {
	t.Parallel()
	// we need this to create the actual process we test
	buf := &lockedBuffer{
		writtenCh: make(chan struct{}),
	}
	npc := &nodeProcessCreator{
		log:         logging.NoLog{},
		stdout:      buf,
		stderr:      buf,
		colorPicker: utils.NewColorPicker(),
	}

	// define a bogus output
	testOutput := "this is the output"
	// we will use `echo` with the testOutput as we will get a measurable result
	ctrlCmd := exec.Command("echo", testOutput)
	// we would not really need to execute the command, just the output would be enough
	// nevertheless let's do it to simulate the actual case
	expectedResult, err := ctrlCmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	// this is the "mock" node name we want to see prepended to the output
	mockNodeName := "redirect-test-node"

	// now create the node process and check it will be prepended and colored
	testConfig := node.Config{
		BinaryPath:     "sh",
		RedirectStdout: true,
		RedirectStderr: true,
		Name:           mockNodeName,
	}
	// Sleep for a second after echoing so that we have a chance to read from the stdout pipe
	// before it closes when the process exits and Wait() returns.
	// See https://pkg.go.dev/os/exec#Cmd.StdoutPipe
	proc, err := npc.NewNodeProcess(testConfig, "-c", fmt.Sprintf("echo %s && sleep 1", testOutput))
	if err != nil {
		t.Fatal(err)
	}

	// lock read access to the buffer
	<-buf.writtenCh
	result := buf.String()

	// wait for the process to finish.
	_ = proc.Stop(context.Background())

	// now do the checks:
	// the new string should contain the node name
	if !strings.Contains(result, mockNodeName) {
		t.Fatalf("expected subcommand to contain node name %s, but it didn't", mockNodeName)
	}

	// and it should have a specific length:
	//             the actual output   + the color terminal escape sequence      + node name    + []<space> + color terminal reset escape sequence
	expectedLen := len(expectedResult) + len(utils.NewColorPicker().NextColor()) + len(mockNodeName) + 3 + len(logging.Reset)
	if len(result) != expectedLen {
		t.Fatalf("expected string length to be %d, but it was %d", expectedLen, len(result))
	}
}

// checkNetwork receives a network, a set of running nodes (started and not removed yet), and
// a set of removed nodes, checking:
// - GetNodeNames retrieves the correct number of running nodes
// - GetNode does not fail for given running nodes
// - GetNode does fail for given stopped nodes
func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]struct{}, removedNodes map[string]struct{}) {
	require := require.New(t)
	nodeNames, err := net.GetNodeNames()
	require.NoError(err)
	require.EqualValues(len(nodeNames), len(runningNodes))
	for nodeName := range runningNodes {
		_, err := net.GetNode(nodeName)
		require.NoError(err)
	}
	for nodeName := range removedNodes {
		_, err := net.GetNode(nodeName)
		require.Error(err)
	}
}

// Return a network config that has no nodes
func emptyNetworkConfig() (network.Config, error) {
	networkID := uint32(1337)
	// Use a dummy genesis
	genesis, err := network.NewAvalancheGoGenesis(
		networkID,
		[]network.AddrAndBalance{
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: big.NewInt(1),
			},
		},
		nil,
		[]ids.NodeID{ids.GenerateTestNodeID()},
	)
	if err != nil {
		return network.Config{}, err
	}
	return network.Config{
		Genesis: string(genesis),
	}, nil
}

// Returns a config for a three node network,
// where the nodes have randomly generated staking
// keys and certificates.
func testNetworkConfig(t *testing.T) network.Config {
	require := require.New(t)
	networkConfig, err := NewDefaultConfigNNodes("pepito", 3)
	require.NoError(err)
	for i := 0; i < 3; i++ {
		networkConfig.NodeConfigs[i].Name = fmt.Sprintf("node%d", i)
		delete(networkConfig.NodeConfigs[i].Flags, config.HTTPPortKey)
		delete(networkConfig.NodeConfigs[i].Flags, config.StakingPortKey)
	}
	return networkConfig
}

// Returns nil when all the nodes in [net] are healthy,
// or an error if one doesn't become healthy within
// the timeout.
func awaitNetworkHealthy(net network.Network, timeout time.Duration) error { //nolint
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return net.Healthy(ctx)
}

func TestAddNetworkFlags(t *testing.T) {
	t.Parallel()
	type test struct {
		name            string
		netFlags        map[string]interface{}
		beforeNodeFlags map[string]interface{}
		afterNodeFlags  map[string]interface{}
	}
	tests := []test{
		{
			name:            "all empty",
			netFlags:        map[string]interface{}{},
			beforeNodeFlags: map[string]interface{}{},
			afterNodeFlags:  map[string]interface{}{},
		},
		{
			name:            "net flags not empty; node flags empty",
			netFlags:        map[string]interface{}{"1": 1},
			beforeNodeFlags: map[string]interface{}{},
			afterNodeFlags:  map[string]interface{}{"1": 1},
		},
		{
			name:            "net flags not empty; node flags not empty",
			netFlags:        map[string]interface{}{"1": 1},
			beforeNodeFlags: map[string]interface{}{"2": 2},
			afterNodeFlags:  map[string]interface{}{"1": 1, "2": 2},
		},
		{
			name:            "net flags empty; node flags not empty",
			netFlags:        map[string]interface{}{},
			beforeNodeFlags: map[string]interface{}{"2": 2},
			afterNodeFlags:  map[string]interface{}{"2": 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			addNetworkFlags(logging.NoLog{}, tt.netFlags, tt.beforeNodeFlags)
			require.Equal(tt.afterNodeFlags, tt.beforeNodeFlags)
		})
	}
}

func TestSetNodeName(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	ln := &localNetwork{
		nodes:          make(map[string]*localNode),
		nextNodeSuffix: 1,
	}

	// Case: No name given
	config := &node.Config{Name: ""}
	err := ln.setNodeName(config)
	require.NoError(err)
	require.Equal("node1", config.Name)

	// Case: No name given again
	config.Name = ""
	err = ln.setNodeName(config)
	require.NoError(err)
	require.Equal("node1", config.Name)

	// Case: name given
	config.Name = "hi"
	err = ln.setNodeName(config)
	require.NoError(err)
	require.Equal("hi", config.Name)

	// Case: name already present
	config.Name = "hi"
	ln.nodes = map[string]*localNode{"hi": nil}
	err = ln.setNodeName(config)
	require.Error(err)
}

func TestGetConfigEntry(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	// case: key not present
	val, err := getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"2": "2"},
		"1",
		"1",
	)
	require.NoError(err)
	require.Equal("1", val)

	// case: key present
	val, err = getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"1": "hi", "2": "2"},
		"1",
		"1",
	)
	require.NoError(err)
	require.Equal("hi", val)

	// case: key present wrong type
	_, err = getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"1": 1, "2": "2"},
		"1",
		"1",
	)
	require.Error(err)
}

func TestGetPort(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	// Case: port key present in config file
	port, err := getPort(
		map[string]interface{}{},
		map[string]interface{}{"flag": float64(10013)},
		"flag",
		false,
	)
	require.NoError(err)
	require.Equal(uint16(10013), port)

	// Case: port key present in flags
	port, err = getPort(
		map[string]interface{}{"flag": 10013},
		map[string]interface{}{},
		"flag",
		false,
	)
	require.NoError(err)
	require.Equal(uint16(10013), port)

	// Case: port key present in config file and flags
	port, err = getPort(
		map[string]interface{}{"flag": 10013},
		map[string]interface{}{"flag": float64(14)},
		"flag",
		false,
	)
	require.NoError(err)
	require.Equal(uint16(10013), port)

	// Case: port key not present
	_, err = getPort(
		map[string]interface{}{},
		map[string]interface{}{},
		"flag",
		false,
	)
	require.NoError(err)
}

func TestCreateFileAndWrite(t *testing.T) {
	t.Parallel()
	require := require.New(t)
	dir, err := os.MkdirTemp("", "network-runner-test-*")
	require.NoError(err)
	path := filepath.Join(dir, "path")
	contents := []byte("hi")
	err = createFileAndWrite(path, contents)
	require.NoError(err)
	gotBytes, err := os.ReadFile(path)
	require.NoError(err)
	require.Equal(contents, gotBytes)
}

func TestWriteFiles(t *testing.T) {
	t.Parallel()
	stakingKey := "stakingKey"
	stakingCert := "stakingCert"
	genesis := []byte("genesis")
	configFile := "config file"
	chainConfigFiles := map[string]string{
		"C": "c-chain config file",
	}
	tmpDir, err := os.MkdirTemp("", "avalanche-network-runner-tests-*")
	if err != nil {
		t.Fatal(err)
	}
	stakingKeyPath := filepath.Join(tmpDir, stakingKeyFileName)
	stakingCertPath := filepath.Join(tmpDir, stakingCertFileName)
	stakingSigningKeyPath := filepath.Join(tmpDir, stakingSigningKeyFileName)
	genesisPath := filepath.Join(tmpDir, genesisFileName)
	configFilePath := filepath.Join(tmpDir, configFileName)
	chainConfigDir := filepath.Join(tmpDir, chainConfigSubDir)
	subnetConfigDir := filepath.Join(tmpDir, subnetConfigSubDir)
	cChainConfigPath := filepath.Join(tmpDir, chainConfigSubDir, "C", configFileName)

	type test struct {
		name          string
		shouldErr     bool
		genesis       []byte
		nodeConfig    node.Config
		expectedFlags map[string]string
	}

	tests := []test{
		{
			name:      "no config files given",
			shouldErr: false,
			genesis:   genesis,
			nodeConfig: node.Config{
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
			},
			expectedFlags: map[string]string{
				config.StakingTLSKeyPathKey:    stakingKeyPath,
				config.StakingCertPathKey:      stakingCertPath,
				config.StakingSignerKeyPathKey: stakingSigningKeyPath,
				config.GenesisConfigFileKey:    genesisPath,
				config.ChainConfigDirKey:       chainConfigDir,
				config.SubnetConfigDirKey:      subnetConfigDir,
			},
		},
		{
			name:      "config file given but not c-chain config file",
			shouldErr: false,
			genesis:   genesis,
			nodeConfig: node.Config{
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
				ConfigFile:  configFile,
			},
			expectedFlags: map[string]string{
				config.StakingTLSKeyPathKey:    stakingKeyPath,
				config.StakingCertPathKey:      stakingCertPath,
				config.StakingSignerKeyPathKey: stakingSigningKeyPath,
				config.GenesisConfigFileKey:    genesisPath,
				config.ChainConfigDirKey:       chainConfigDir,
				config.SubnetConfigDirKey:      subnetConfigDir,
				config.ConfigFileKey:           configFilePath,
			},
		},
		{
			name:      "config file and c-chain config file given",
			shouldErr: false,
			genesis:   genesis,
			nodeConfig: node.Config{
				StakingKey:       stakingKey,
				StakingCert:      stakingCert,
				ConfigFile:       configFile,
				ChainConfigFiles: chainConfigFiles,
			},
			expectedFlags: map[string]string{
				config.StakingTLSKeyPathKey:    stakingKeyPath,
				config.StakingCertPathKey:      stakingCertPath,
				config.StakingSignerKeyPathKey: stakingSigningKeyPath,
				config.GenesisConfigFileKey:    genesisPath,
				config.ChainConfigDirKey:       chainConfigDir,
				config.SubnetConfigDirKey:      subnetConfigDir,
				config.ConfigFileKey:           configFilePath,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			flags, err := writeFiles(tt.genesis, tmpDir, &tt.nodeConfig)
			if tt.shouldErr {
				require.Error(err)
				return
			}
			require.NoError(err)
			// Make sure returned flags are right
			require.Len(tt.expectedFlags, len(flags))
			for k := range flags {
				require.Equal(tt.expectedFlags[k], flags[k])
			}
			// Assert files created correctly
			gotStakingKey, err := os.ReadFile(stakingKeyPath)
			require.NoError(err)
			require.Equal([]byte(tt.nodeConfig.StakingKey), gotStakingKey)
			gotStakingCert, err := os.ReadFile(stakingCertPath)
			require.NoError(err)
			require.Equal([]byte(tt.nodeConfig.StakingCert), gotStakingCert)
			gotGenesis, err := os.ReadFile(genesisPath)
			require.NoError(err)
			require.Equal(tt.genesis, gotGenesis)
			if len(tt.nodeConfig.ConfigFile) > 0 {
				gotConfigFile, err := os.ReadFile(configFilePath)
				require.NoError(err)
				require.Equal([]byte(configFile), gotConfigFile)
			}
			if tt.nodeConfig.ChainConfigFiles != nil {
				gotCChainConfigFile, err := os.ReadFile(cChainConfigPath)
				require.NoError(err)
				require.Equal([]byte(chainConfigFiles["C"]), gotCChainConfigFile)
			}
		})
	}
}

func TestRemoveBeacon(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	// create a network with no nodes in it
	emptyNetworkConfig, err := emptyNetworkConfig()
	require.NoError(err)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	require.NoError(err)

	// a network config for a 3 node staking network, and add the bootstrapper
	// to the exesting network
	networkConfig := testNetworkConfig(t)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	require.NoError(err)

	// remove the beacon node from the network
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	require.NoError(err)
	require.Equal(0, net.bootstraps.Len())
}

// Returns an API client where:
// * The Health API's Health method always returns an error after the
//   given context is cancelled.
// * The CChainEthAPI's Close method may be called
// * Only the above 2 methods may be called
func newMockAPIHealthyBlocks(string, uint16) api.Client {
	healthClient := &healthmocks.Client{}
	healthClient.On("Health", mock.MatchedBy(func(_ context.Context) bool {
		return true
	}), mock.Anything).Return(
		func(ctx context.Context, _ ...rpc.Option) *health.APIReply {
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context, _ ...rpc.Option) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	// ethClient used when removing nodes, to close websocket connection
	ethClient := &apimocks.EthClient{}
	ethClient.On("Close").Return()
	client := &apimocks.Client{}
	client.On("HealthAPI").Return(healthClient)
	client.On("CChainEthAPI").Return(ethClient)
	return client
}

// Assert that if the network's Stop method is called while
// a call to Healthy is ongoing, Healthy returns immediately.
func TestHealthyDuringNetworkStop(t *testing.T) {
	require := require.New(t)
	networkConfig := testNetworkConfig(t)
	// Calls to a node's Healthy() function blocks until context cancelled
	net, err := newNetwork(logging.NoLog{}, newMockAPIHealthyBlocks, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	require.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	require.NoError(err)

	healthyChan := make(chan error)
	go func() {
		healthyChan <- net.Healthy(context.Background())
	}()
	// Wait to make sure we're actually blocking on Health API call
	time.Sleep(500 * time.Millisecond)
	err = net.Stop(context.Background())
	require.NoError(err)
	select {
	case err := <-healthyChan:
		require.Error(err)
	case <-time.After(1 * time.Second):
		// Since [net.Stop] was called, [net.Healthy] should immediately return.
		// We assume that it will do so within 1 second.
		require.Fail("Healthy should've returned immediately because network closed")
	}
}
