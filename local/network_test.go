package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/local/mocks"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

var _ NewNodeProcessF = newMockProcessUndef
var _ NewNodeProcessF = newMockProcessSuccessful
var _ NewNodeProcessF = newMockProcessFailedStart

func newMockProcessUndef(node.Config, ...string) (NodeProcess, error) {
	return &mocks.NodeProcess{}, nil
}

func newMockProcessSuccessful(node.Config, ...string) (NodeProcess, error) {
	process := &mocks.NodeProcess{}
	process.On("Start").Return(nil)
	process.On("Wait").Return(nil)
	process.On("Stop").Return(nil)
	return process, nil
}

func newMockProcessFailedStart(node.Config, ...string) (NodeProcess, error) {
	process := &mocks.NodeProcess{}
	process.On("Start").Return(errors.New("Start failed"))
	process.On("Wait").Return(nil)
	process.On("Stop").Return(nil)
	return process, nil
}

// Start a network with no nodes
func TestNewNetworkEmpty(t *testing.T) {
	assert := assert.New(t)
	networkID := uint32(1337)
	// Use a dummy genesis
	genesis, err := network.NewAvalancheGoGenesis(
		logging.NoLog{},
		networkID,
		[]network.AddrAndBalance{
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: 1,
			},
		},
		nil,
		[]ids.ShortID{ids.GenerateTestShortID()},
	)
	assert.NoError(err)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	networkConfig.NetworkID = networkID
	networkConfig.Genesis = genesis
	networkConfig.NodeConfigs = nil
	net, err := NewNetwork(
		logging.NoLog{},
		networkConfig,
		api.NewAPIClient, // TODO change AvalancheGo so we can mock API clients
		newMockProcessUndef,
	)
	assert.NoError(err)
	// Assert that GetNodesNames() includes only the 1 node's name
	names, err := net.GetNodesNames()
	assert.NoError(err)
	assert.Len(names, 0)
}

// Start a network with one node.
func TestNewNetworkOneNode(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	networkConfig.NodeConfigs = networkConfig.NodeConfigs[:1]
	// Assert that the node's config is being passed correctly
	// to the function that starts the node process.
	newProcessF := func(config node.Config, _ ...string) (NodeProcess, error) {
		assert.True(config.IsBeacon)
		assert.EqualValues(networkConfig.NodeConfigs[0], config)
		return newMockProcessSuccessful(config)
	}
	net, err := NewNetwork(
		logging.NoLog{},
		networkConfig,
		api.NewAPIClient,
		newProcessF,
	)
	assert.NoError(err)

	// Assert that GetNodesNames() includes only the 1 node's name
	names, err := net.GetNodesNames()
	assert.NoError(err)
	assert.Contains(names, networkConfig.NodeConfigs[0].Name)
	assert.Len(names, 1)

	// Assert that the network's genesis was set
	assert.EqualValues(networkConfig.Genesis, net.(*localNetwork).genesis)
}

// Check configs that are expected to be invalid at network creation time
func TestWrongNetworkConfigs(t *testing.T) {
	tests := map[string]struct {
		input network.Config
	}{
		"no ImplSpecificConfig": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
				},
			},
		}},
		"no ConfigFile": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon: true,
				},
			},
		}},
		"no Genesis": {input: network.Config{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
				},
			},
		}},
		"StakingKey but no StakingCert": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
					StakingKey: []byte("nonempty"),
				},
			},
		}},
		"StakingCert but no StakingKey": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					ConfigFile:  []byte("nonempty"),
					StakingCert: []byte("nonempty"),
				},
			},
		}},
		"no beacon node": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					ConfigFile: []byte("nonempty"),
				},
			},
		}},
		"repeated name": {input: network.Config{
			Genesis: []byte("nonempty"),
			NodeConfigs: []node.Config{
				{
					Name: "node0",
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
				},
				{
					Name: "node0",
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
				},
			},
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			_, err := NewNetwork(logging.NoLog{}, tc.input, api.NewAPIClient, newMockProcessSuccessful)
			assert.Error(err)
		})
	}
}

// Give incorrect type to interface{} ImplSpecificConfig
func TestImplSpecificConfigInterface(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	networkConfig.NodeConfigs[0].ImplSpecificConfig = "should not be string"
	_, err = NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.Error(err)
}

/*
// Creates a network and fake health api response so network Healthy also fails
// TODO: needs to set fake fail health api. uncomment after that.
func TestUnhealthyNetwork(t *testing.T) {
	t.Skip()
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	_, err = NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	assert.Error(awaitNetworkHealthy(net))
}
*/

// Create a network not giving names to nodes, checks the generated names are the
// correct number and all different
func TestGeneratedNodesNames(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	for i := range networkConfig.NodeConfigs {
		networkConfig.NodeConfigs[i].Name = ""
	}
	net, err := NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	nodeNameMap := make(map[string]bool)
	nodeNames, err := net.GetNodesNames()
	assert.NoError(err)
	for _, nodeName := range nodeNames {
		nodeNameMap[nodeName] = true
	}
	assert.EqualValues(len(nodeNameMap), len(networkConfig.NodeConfigs))
}

// TODO add byzantine node to conf
// TestNetworkFromConfig creates/waits/checks/stops a network from config file
// the check verify that all the nodes can be accessed
/*
// TODO: needs to set fake successful health api. uncomment after that.
func TestNetworkFromConfig(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	assert.NoError(awaitNetworkHealthy(net))
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = true
	}
	checkNetwork(t, net, runningNodes, nil)
}
*/

// TestNetworkNodeOps creates/waits/checks/stops a network created from an empty one
// nodes are first added one by one, then removed one by one. between all operations, a network check is performed
// the check verify that all the started nodes are accessible, and all removed nodes are not accessible
// all nodes are taken from config file
/*
// TODO: needs to set fake successful health api. uncomment after that.
func TestNetworkNodeOps(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, network.Config{}, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err = net.AddNode(nodeConfig)
		assert.NoError(err)
		runningNodes[nodeConfig.Name] = true
		checkNetwork(t, net, runningNodes, nil)
	}
	assert.NoError(awaitNetworkHealthy(net))
	removedNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err := net.GetNode(nodeConfig.Name)
		assert.NoError(err)
		err = net.RemoveNode(nodeConfig.Name)
		assert.NoError(err)
		removedNodes[nodeConfig.Name] = true
		delete(runningNodes, nodeConfig.Name)
		checkNetwork(t, net, runningNodes, removedNodes)
	}
}
*/

// TestNodeNotFound checks all operations fail for an unknown node,
// being it either not created, or created and removed thereafter
func TestNodeNotFound(t *testing.T) {
	assert := assert.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, emptyNetworkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	assert.NoError(err)
	// get correct node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.NoError(err)
	// get uncorrect node (non created)
	_, err = net.GetNode(networkConfig.NodeConfigs[1].Name)
	assert.Error(err)
	// remove uncorrect node (non created)
	err = net.RemoveNode(networkConfig.NodeConfigs[1].Name)
	assert.Error(err)
	// remove correct node
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	assert.NoError(err)
	// get uncorrect node (removed)
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.Error(err)
	// remove uncorrect node (removed)
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	assert.Error(err)
}

// TestStoppedNetwork checks that operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	assert := assert.New(t)
	emptyNetworkConfig, err := emptyNetworkConfig()
	assert.NoError(err)
	networkConfig, err := defaultNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, emptyNetworkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	assert.NoError(err)
	// first GetNodesNames should return some nodes
	_, err = net.GetNodesNames()
	assert.NoError(err)
	err = net.Stop(context.TODO())
	assert.NoError(err)
	// Stop failure
	assert.EqualValues(net.Stop(context.TODO()), errStopped)
	// AddNode failure
	_, err = net.AddNode(networkConfig.NodeConfigs[1])
	assert.EqualValues(err, errStopped)
	// GetNode failure
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	assert.EqualValues(err, errStopped)
	// second GetNodesNames should return no nodes
	_, err = net.GetNodesNames()
	assert.EqualValues(err, errStopped)
	// RemoveNode failure
	assert.EqualValues(net.RemoveNode(networkConfig.NodeConfigs[0].Name), errStopped)
	// Healthy failure
	assert.EqualValues(awaitNetworkHealthy(net), errStopped)
}

// checkNetwork receives a network, a set of running nodes (started and not removed yet), and
// a set of removed nodes, checking:
// - GetNodeNames retrieves the correct number of running nodes
// - GetNode does not fail for given running nodes
// - GetNode does fail for given stopped nodes
/*
// TODO: uncomment when associated tests are uncommented when having fake health apis
func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]bool, removedNodes map[string]bool) {
	assert := assert.New(t)
	nodeNames, err := net.GetNodesNames()
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
*/

func awaitNetworkHealthy(net network.Network) error {
	healthyCh := net.Healthy()
	err, ok := <-healthyCh
	if ok {
		return err
	}
	return nil
}

func emptyNetworkConfig() (network.Config, error) {
	// TODO remove test files when we can auto-generate genesis
	// and other files
	genesisFile, err := os.ReadFile("test_files/genesis.json")
	if err != nil {
		return network.Config{}, err
	}
	return network.Config{
		NetworkID: uint32(0),
		LogLevel:  "DEBUG",
		Name:      "My Network",
		Genesis:   genesisFile,
	}, nil
}

func defaultNetworkConfig() (network.Config, error) {
	// TODO remove test files when we can auto-generate genesis
	// and other files
	networkConfig, err := emptyNetworkConfig()
	if err != nil {
		return networkConfig, err
	}
	cchainConfigFile, err := os.ReadFile("test_files/cchain_config.json")
	if err != nil {
		return networkConfig, err
	}
	for i := 1; i <= 6; i++ {
		nodeConfig := node.Config{
			CChainConfigFile: cchainConfigFile,
			Name:             fmt.Sprintf("node%d", i),
		}
		configFile, err := os.ReadFile(fmt.Sprintf("test_files/node%d/config.json", i))
		if err != nil {
			return networkConfig, err
		}
		nodeConfig.ConfigFile = configFile
		certFile, err := os.ReadFile(fmt.Sprintf("test_files/node%d/staking.crt", i))
		if err == nil {
			nodeConfig.StakingCert = certFile
		}
		keyFile, err := os.ReadFile(fmt.Sprintf("test_files/node%d/staking.key", i))
		if err == nil {
			nodeConfig.StakingKey = keyFile
		}
		if nodeConfig.StakingCert == nil {
			nodeConfig.StakingCert, nodeConfig.StakingKey, err = staking.NewCertAndKeyBytes()
			if err != nil {
				return networkConfig, err
			}
		}
		localNodeConf := NodeConfig{
			BinaryPath: "pepito",
			Stdout:     os.Stdout,
			Stderr:     os.Stderr,
		}
		nodeConfig.ImplSpecificConfig = localNodeConf
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfig.NodeConfigs[0].IsBeacon = true
	return networkConfig, nil
}
