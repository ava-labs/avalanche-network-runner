package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	apimocks "github.com/ava-labs/avalanche-network-runner/api/mocks"
	"github.com/ava-labs/avalanche-network-runner/local/mocks"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/health"
	healthmocks "github.com/ava-labs/avalanchego/api/health/mocks"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const defaultHealthyTimeout = 5 * time.Second

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

type localTestFailedStartProcessCreator struct{}

func (*localTestFailedStartProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	return nil, errors.New("error on purpose for test")
}

type localTestProcessUndefNodeProcessCreator struct{}

func (*localTestProcessUndefNodeProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	return newMockProcessUndef(config, flags...)
}

type localTestFlagCheckProcessCreator struct {
	expectedFlags map[string]interface{}
	assert        *assert.Assertions
}

func (lt *localTestFlagCheckProcessCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	if ok := lt.assert.EqualValues(lt.expectedFlags, config.Flags); !ok {
		return nil, errors.New("assertion failed: flags not equal value")
	}
	return newMockProcessSuccessful(config, flags...)
}

// Returns an API client where:
// * The Health API's Health method always returns healthy
// * The CChainEthAPI's Close method may be called
// * Only the above 2 methods may be called
// TODO have this method return an API Client that has all
// APIs and methods implemented
func newMockAPISuccessful(ipAddr string, port uint16) api.Client {
	healthReply := &health.APIHealthReply{Healthy: true}
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
func newMockAPIUnhealthy(ipAddr string, port uint16) api.Client {
	healthReply := &health.APIHealthReply{Healthy: false}
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

func (*noOpInboundHandler) HandleInbound(message.InboundMessage) {}

// Start a network with no nodes
func TestNewNetworkEmpty(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
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
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	// Assert that GetNodeNames() returns an empty list
	names, err := net.GetNodeNames()
	assert.NoError(err)
	assert.Len(names, 0)
}

type localTestOneNodeCreator struct {
	assert         *assert.Assertions
	networkConfig  network.Config
	successCreator *localTestSuccessfulNodeProcessCreator
}

func newLocalTestOneNodeCreator(assert *assert.Assertions, networkConfig network.Config) *localTestOneNodeCreator {
	return &localTestOneNodeCreator{
		assert:         assert,
		networkConfig:  networkConfig,
		successCreator: &localTestSuccessfulNodeProcessCreator{},
	}
}

// Assert that the node's config is being passed correctly
// to the function that starts the node process.
func (lt *localTestOneNodeCreator) NewNodeProcess(config node.Config, flags ...string) (NodeProcess, error) {
	lt.assert.True(config.IsBeacon)
	expectedConfig := lt.networkConfig.NodeConfigs[0]
	lt.assert.EqualValues(lt.networkConfig.ChainConfigFiles, config.ChainConfigFiles)
	lt.assert.EqualValues(expectedConfig.ConfigFile, config.ConfigFile)
	lt.assert.EqualValues(lt.networkConfig.BinaryPath, config.BinaryPath)
	lt.assert.EqualValues(expectedConfig.IsBeacon, config.IsBeacon)
	lt.assert.EqualValues(expectedConfig.Name, config.Name)
	lt.assert.EqualValues(expectedConfig.StakingCert, config.StakingCert)
	lt.assert.EqualValues(expectedConfig.StakingKey, config.StakingKey)
	lt.assert.Len(config.Flags, len(expectedConfig.Flags))
	for k, v := range expectedConfig.Flags {
		gotV, ok := config.Flags[k]
		lt.assert.True(ok)
		lt.assert.EqualValues(v, gotV)
	}
	return lt.successCreator.NewNodeProcess(config, flags...)
}

// Start a network with one node.
func TestNewNetworkOneNode(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	networkConfig.NodeConfigs = networkConfig.NodeConfigs[:1]
	creator := newLocalTestOneNodeCreator(assert, networkConfig)
	net, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		creator,
		"",
		"",
		false,
	)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)

	// Assert that GetNodeNames() includes only the 1 node's name
	names, err := net.GetNodeNames()
	assert.NoError(err)
	assert.Contains(names, networkConfig.NodeConfigs[0].Name)
	assert.Len(names, 1)

	// Assert that the network's genesis was set
	assert.EqualValues(networkConfig.Genesis, net.genesis)
}

// Test that NewNetwork returns an error when
// starting a node returns an error
func TestNewNetworkFailToStartNode(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(
		logging.NoLog{},
		newMockAPISuccessful,
		&localTestFailedStartProcessCreator{},
		"",
		"",
		false,
	)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.Error(err)
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
	assert := assert.New(t)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
			assert.NoError(err)
			err = net.loadConfig(context.Background(), tt.config)
			assert.Error(err)
		})
	}
}

// Assert that the network's Healthy() method returns an
// error when all nodes' Health API return unhealthy
func TestUnhealthyNetwork(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPIUnhealthy, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	assert.Error(awaitNetworkHealthy(net, defaultHealthyTimeout))
}

// Create a network without giving names to nodes.
// Checks that the generated names are the correct number and unique.
func TestGeneratedNodesNames(t *testing.T) {
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	for i := range networkConfig.NodeConfigs {
		networkConfig.NodeConfigs[i].Name = ""
	}
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	nodeNameMap := make(map[string]bool)
	nodeNames, err := net.GetNodeNames()
	assert.NoError(err)
	for _, nodeName := range nodeNames {
		nodeNameMap[nodeName] = true
	}
	assert.EqualValues(len(nodeNameMap), len(networkConfig.NodeConfigs))
}

// TestGenerateDefaultNetwork create a default network with config from NewDefaultConfig and
// check expected number of nodes, node names, and avalanchego node ids
func TestGenerateDefaultNetwork(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	binaryPath := "pepito"
	networkConfig := NewDefaultConfig(binaryPath)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	assert.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))
	names, err := net.GetNodeNames()
	assert.NoError(err)
	assert.Len(names, 5)
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
		assert.Contains(names, nodeInfo.name)
		node, err := net.GetNode(nodeInfo.name)
		assert.NoError(err)
		assert.EqualValues(nodeInfo.name, node.GetName())
		expectedID, err := ids.NodeIDFromString(nodeInfo.ID)
		assert.NoError(err)
		assert.EqualValues(expectedID, node.GetNodeID())
	}
}

// TODO add byzantine node to conf
// TestNetworkFromConfig creates/waits/checks/stops a network from config file
// the check verify that all the nodes can be accessed
func TestNetworkFromConfig(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	assert.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))
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
	assert := assert.New(t)

	// Start a new, empty network
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	assert.NoError(err)
	runningNodes := make(map[string]struct{})

	// Add nodes to the network one by one
	networkConfig := testNetworkConfig(t)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.AddNode(nodeConfig)
		assert.NoError(err)
		runningNodes[nodeConfig.Name] = struct{}{}
		checkNetwork(t, net, runningNodes, nil)
	}
	// Wait for all nodes to be healthy
	assert.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))

	// Remove nodes one by one
	removedNodes := make(map[string]struct{})
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.GetNode(nodeConfig.Name)
		assert.NoError(err)
		err = net.RemoveNode(context.Background(), nodeConfig.Name)
		assert.NoError(err)
		removedNodes[nodeConfig.Name] = struct{}{}
		delete(runningNodes, nodeConfig.Name)
		checkNetwork(t, net, runningNodes, removedNodes)
	}
}

// TestNodeNotFound checks all operations fail for an unknown node,
// being it either not created, or created and removed thereafter
func TestNodeNotFound(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	assert.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	assert.NoError(err)
	// get node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.NoError(err)
	// get non-existent node
	_, err = net.GetNode(networkConfig.NodeConfigs[1].Name)
	assert.Error(err)
	// remove non-existent node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[1].Name)
	assert.Error(err)
	// remove node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	assert.NoError(err)
	// get removed node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.Error(err)
	// remove already-removed node
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	assert.Error(err)
}

// TestStoppedNetwork checks that operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), emptyNetworkConfig)
	assert.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	assert.NoError(err)
	// first GetNodeNames should return some nodes
	_, err = net.GetNodeNames()
	assert.NoError(err)
	err = net.Stop(context.Background())
	assert.NoError(err)
	// Stop failure
	assert.EqualValues(net.Stop(context.Background()), network.ErrStopped)
	// AddNode failure
	_, err = net.AddNode(networkConfig.NodeConfigs[1])
	assert.EqualValues(network.ErrStopped, err)
	// GetNode failure
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.EqualValues(err, network.ErrStopped)
	// second GetNodeNames should return no nodes
	_, err = net.GetNodeNames()
	assert.EqualValues(network.ErrStopped, err)
	// RemoveNode failure
	assert.EqualValues(network.ErrStopped, net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name))
	// Healthy failure
	assert.EqualValues(awaitNetworkHealthy(net, defaultHealthyTimeout), network.ErrStopped)
	_, err = net.GetAllNodes()
	assert.EqualValues(err, network.ErrStopped)
}

func TestGetAllNodes(t *testing.T) {
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)

	nodes, err := net.GetAllNodes()
	assert.NoError(err)
	assert.Len(nodes, len(net.nodes))
	for name, node := range net.nodes {
		assert.EqualValues(node, nodes[name])
	}
}

// TestFlags tests that we can pass flags through the network.Config
// but also via node.Config and that the latter overrides the former
// if same keys exist.
func TestFlags(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
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
			assert:        assert,
		},
		"",
		"",
		false,
	)
	assert.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	if ok := assert.NoError(err); !ok {
		t.Fatal("assertion failed")
	}
	err = nw.Stop(context.Background())
	assert.NoError(err)

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
			assert:        assert,
		},
		"",
		"",
		false,
	)
	assert.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	if ok := assert.NoError(err); !ok {
		t.Fatal("assertion failed")
	}
	err = nw.Stop(context.Background())
	assert.NoError(err)

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
			assert:        assert,
		},
		"",
		"",
		false,
	)
	assert.NoError(err)
	err = nw.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)
	err = nw.Stop(context.Background())
	assert.NoError(err)
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
	// we would not really need to execute the command, just the ouput would be enough
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
	assert := assert.New(t)
	nodeNames, err := net.GetNodeNames()
	assert.NoError(err)
	assert.EqualValues(len(nodeNames), len(runningNodes))
	for nodeName := range runningNodes {
		_, err := net.GetNode(nodeName)
		assert.NoError(err)
	}
	for nodeName := range removedNodes {
		_, err := net.GetNode(nodeName)
		assert.Error(err)
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
				Balance: 1,
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
	assert := assert.New(t)
	networkConfig, err := NewDefaultConfigNNodes("pepito", 3)
	assert.NoError(err)
	for i := 0; i < 3; i++ {
		networkConfig.NodeConfigs[i].Name = fmt.Sprintf("node%d", i)
	}
	return networkConfig
}

// Returns nil when all the nodes in [net] are healthy,
// or an error if one doesn't become healthy within
// the timeout.
func awaitNetworkHealthy(net network.Network, timeout time.Duration) error {
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
			assert := assert.New(t)
			addNetworkFlags(logging.NoLog{}, tt.netFlags, tt.beforeNodeFlags)
			assert.Equal(tt.afterNodeFlags, tt.beforeNodeFlags)
		})
	}
}

func TestSetNodeName(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	ln := &localNetwork{
		nodes:          make(map[string]*localNode),
		nextNodeSuffix: 1,
	}

	// Case: No name given
	config := &node.Config{Name: ""}
	err := ln.setNodeName(config)
	assert.NoError(err)
	assert.Equal("node1", config.Name)

	// Case: No name given again
	config.Name = ""
	err = ln.setNodeName(config)
	assert.NoError(err)
	assert.Equal("node1", config.Name)

	// Case: name given
	config.Name = "hi"
	err = ln.setNodeName(config)
	assert.NoError(err)
	assert.Equal("hi", config.Name)

	// Case: name already present
	config.Name = "hi"
	ln.nodes = map[string]*localNode{"hi": nil}
	err = ln.setNodeName(config)
	assert.Error(err)
}

func TestGetConfigEntry(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// case: key not present
	val, err := getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"2": "2"},
		"1",
		"1",
	)
	assert.NoError(err)
	assert.Equal("1", val)

	// case: key present
	val, err = getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"1": "hi", "2": "2"},
		"1",
		"1",
	)
	assert.NoError(err)
	assert.Equal("hi", val)

	// case: key present wrong type
	_, err = getConfigEntry(
		map[string]interface{}{},
		map[string]interface{}{"1": 1, "2": "2"},
		"1",
		"1",
	)
	assert.Error(err)
}

func TestGetPort(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// Case: port key present in config file
	port, err := getPort(
		map[string]interface{}{},
		map[string]interface{}{"flag": float64(10013)},
		"flag",
		false,
	)
	assert.NoError(err)
	assert.Equal(uint16(10013), port)

	// Case: port key present in flags
	port, err = getPort(
		map[string]interface{}{"flag": 10013},
		map[string]interface{}{},
		"flag",
		false,
	)
	assert.NoError(err)
	assert.Equal(uint16(10013), port)

	// Case: port key present in config file and flags
	port, err = getPort(
		map[string]interface{}{"flag": 10013},
		map[string]interface{}{"flag": float64(14)},
		"flag",
		false,
	)
	assert.NoError(err)
	assert.Equal(uint16(10013), port)

	// Case: port key not present
	_, err = getPort(
		map[string]interface{}{},
		map[string]interface{}{},
		"flag",
		false,
	)
	assert.NoError(err)
}

func TestCreateFileAndWrite(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	dir, err := os.MkdirTemp("", "network-runner-test-*")
	assert.NoError(err)
	path := filepath.Join(dir, "path")
	contents := []byte("hi")
	err = createFileAndWrite(path, contents)
	assert.NoError(err)
	gotBytes, err := os.ReadFile(path)
	assert.NoError(err)
	assert.Equal(contents, gotBytes)
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
	stakingKeyFlag := fmt.Sprintf("--%s=%v", config.StakingKeyPathKey, stakingKeyPath)
	stakingCertPath := filepath.Join(tmpDir, stakingCertFileName)
	stakingCertFlag := fmt.Sprintf("--%s=%v", config.StakingCertPathKey, stakingCertPath)
	genesisPath := filepath.Join(tmpDir, genesisFileName)
	genesisFlag := fmt.Sprintf("--%s=%v", config.GenesisConfigFileKey, genesisPath)
	configFilePath := filepath.Join(tmpDir, configFileName)
	configFileFlag := fmt.Sprintf("--%s=%v", config.ConfigFileKey, configFilePath)
	chainConfigDir := filepath.Join(tmpDir, chainConfigSubDir)
	cChainConfigPath := filepath.Join(tmpDir, chainConfigSubDir, "C", configFileName)
	chainConfigDirFlag := fmt.Sprintf("--%s=%v", config.ChainConfigDirKey, chainConfigDir)

	type test struct {
		name          string
		shouldErr     bool
		genesis       []byte
		nodeConfig    node.Config
		expectedFlags []string
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
			expectedFlags: []string{
				stakingKeyFlag,
				stakingCertFlag,
				genesisFlag,
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
			expectedFlags: []string{
				stakingKeyFlag,
				stakingCertFlag,
				genesisFlag,
				configFileFlag,
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
			expectedFlags: []string{
				stakingKeyFlag,
				stakingCertFlag,
				genesisFlag,
				configFileFlag,
				chainConfigDirFlag,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			flags, err := writeFiles(tt.genesis, tmpDir, &tt.nodeConfig)
			if tt.shouldErr {
				assert.Error(err)
				return
			}
			assert.NoError(err)
			// Make sure returned flags are right
			assert.ElementsMatch(tt.expectedFlags, flags)
			// Assert files created correctly
			gotStakingKey, err := os.ReadFile(stakingKeyPath)
			assert.NoError(err)
			assert.Equal([]byte(tt.nodeConfig.StakingKey), gotStakingKey)
			gotStakingCert, err := os.ReadFile(stakingCertPath)
			assert.NoError(err)
			assert.Equal([]byte(tt.nodeConfig.StakingCert), gotStakingCert)
			gotGenesis, err := os.ReadFile(genesisPath)
			assert.NoError(err)
			assert.Equal(tt.genesis, gotGenesis)
			if len(tt.nodeConfig.ConfigFile) > 0 {
				gotConfigFile, err := os.ReadFile(configFilePath)
				assert.NoError(err)
				assert.Equal([]byte(configFile), gotConfigFile)
			}
			if tt.nodeConfig.ChainConfigFiles != nil {
				gotCChainConfigFile, err := os.ReadFile(cChainConfigPath)
				assert.NoError(err)
				assert.Equal([]byte(chainConfigFiles["C"]), gotCChainConfigFile)
			}
		})
	}
}

func TestRemoveBeacon(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	// create a network with no nodes in it
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	net, err := newNetwork(logging.NoLog{}, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	net.loadConfig(context.Background(), emptyNetworkConfig)
	assert.NoError(err)

	// a network config for a 3 node staking network, and add the bootstrapper
	// to the exesting network
	networkConfig := testNetworkConfig(t)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	assert.NoError(err)

	// remove the beacon node from the network
	err = net.RemoveNode(context.Background(), networkConfig.NodeConfigs[0].Name)
	assert.NoError(err)
	assert.Equal(0, net.bootstraps.Len())
}

// Returns an API client where:
// * The Health API's Health method always returns an error after the
//   given context is cancelled.
// * The CChainEthAPI's Close method may be called
// * Only the above 2 methods may be called
func newMockAPIHealthyBlocks(ipAddr string, port uint16) api.Client {
	healthClient := &healthmocks.Client{}
	healthClient.On("Health", mock.MatchedBy(func(_ context.Context) bool { return true }), mock.Anything).Return(
		func(ctx context.Context, _ ...rpc.Option) *health.APIHealthReply {
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
	assert := assert.New(t)
	networkConfig := testNetworkConfig(t)
	// Calls to a node's Healthy() function blocks until context cancelled
	net, err := newNetwork(logging.NoLog{}, newMockAPIHealthyBlocks, &localTestSuccessfulNodeProcessCreator{}, "", "", false)
	assert.NoError(err)
	err = net.loadConfig(context.Background(), networkConfig)
	assert.NoError(err)

	healthyChan := make(chan error)
	go func() {
		healthyChan <- net.Healthy(context.Background())
	}()
	// Wait to make sure we're actually blocking on Health API call
	time.Sleep(500 * time.Millisecond)
	err = net.Stop(context.Background())
	assert.NoError(err)
	select {
	case err := <-healthyChan:
		assert.Error(err)
	case <-time.After(1 * time.Second):
		// Since [net.Stop] was called, [net.Healthy] should immediately return.
		// We assume that it will do so within 1 second.
		assert.Fail("Healthy should've returned immediately because network closed")
	}
}
