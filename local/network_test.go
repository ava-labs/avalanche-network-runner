package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/local/mocks"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
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

func TestNewNetworkEmpty(t *testing.T) {
	assert := assert.New(t)
	config := network.Config{
		NodeConfigs: nil,
		LogLevel:    "DEBUG",
		Name:        "My Network",
	}
	net, err := NewNetwork(
		logging.NoLog{},
		config,
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
	binaryPath := "yeet"
	nodeName := "Bob"
	// TODO remove test files when we can auto-generate genesis
	// and other files
	genesis, err := os.ReadFile("test_files/test_genesis.json")
	assert.NoError(err)
	avalancheGoConfig, err := os.ReadFile("test_files/config.json")
	assert.NoError(err)
	nodeConfig := node.Config{
		ImplSpecificConfig: NodeConfig{
			BinaryPath: binaryPath,
		},
		ConfigFile:  avalancheGoConfig,
		Name:        nodeName,
		IsBeacon:    true,
		GenesisFile: genesis,
	}
	config := network.Config{
		NodeCount:   1,
		NodeConfigs: []node.Config{nodeConfig},
		LogLevel:    "DEBUG",
		Name:        "My Network",
	}
	// Assert that the node's config is being passed correctly
	// to the function that starts the node process.
	newProcessF := func(config node.Config, _ ...string) (NodeProcess, error) {
		assert.EqualValues(nodeName, config.Name)
		assert.True(config.IsBeacon)
		assert.EqualValues(genesis, config.GenesisFile)
		assert.EqualValues(avalancheGoConfig, config.ConfigFile)
		assert.EqualValues(binaryPath, config.ImplSpecificConfig.(NodeConfig).BinaryPath)
		process := &mocks.NodeProcess{}
		process.On("Start").Return(nil)
		return process, nil
	}
	net, err := NewNetwork(
		logging.NoLog{},
		config,
		api.NewAPIClient,
		newProcessF,
	)
	assert.NoError(err)
	// Assert that GetNodesNames() includes only the 1 node's name
	names, err := net.GetNodesNames()
	assert.NoError(err)
	assert.Contains(names, nodeName)
	assert.Len(names, 1)
}

func TestWrongNetworkConfigs(t *testing.T) {
	assert := assert.New(t)
	networkConfigs := []network.Config{
		// no ImplSpecificConfig
		{
			NodeConfigs: []node.Config{
				{
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
				},
			},
		},
		// no ConfigFile
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
				},
			},
		},
		// no GenesisFile
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:   true,
					ConfigFile: []byte("nonempty"),
				},
			},
		},
		// StakingKey but no StakingCert
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
					StakingKey:  []byte("nonempty"),
				},
			},
		},
		// StakingCert but no StakingKey
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
					StakingCert: []byte("nonempty"),
				},
			},
		},
		// different genesis file
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty1"),
					ConfigFile:  []byte("nonempty"),
				},
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty2"),
					ConfigFile:  []byte("nonempty"),
				},
			},
		},
		// no beacon node
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
				},
			},
		},
		// repeated name
		{
			NodeConfigs: []node.Config{
				{
					Name: "node0",
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
				},
				{
					Name: "node0",
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
				},
			},
		},
		// invalid cert/key format
		{
			NodeConfigs: []node.Config{
				{
					ImplSpecificConfig: NodeConfig{
						BinaryPath: "pepe",
					},
					IsBeacon:    true,
					GenesisFile: []byte("nonempty"),
					ConfigFile:  []byte("nonempty"),
					StakingCert: []byte("nonempty"),
					StakingKey:  []byte("nonempty"),
				},
			},
		},
	}
	for _, networkConfig := range networkConfigs {
		_, err := NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
		assert.Error(err)
	}
}

func TestImplSpecificConfigInterface(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
	assert.NoError(err)
	networkConfig.NodeConfigs[0].ImplSpecificConfig = "should not be string"
	_, err = NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.Error(err)
}

func TestUnhealthyNetwork(t *testing.T) {
	t.Skip()
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
	assert.NoError(err)
	_, err = NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	// TODO: needs to set fake fail health api
	//assert.Error(awaitNetwork(net))
}

func TestGeneratedNodesNames(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
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
// the check verify that all the nodes api clients are up
func TestNetworkFromConfig(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, networkConfig, api.NewAPIClient, newMockProcessSuccessful)
	assert.NoError(err)
	// TODO: needs to set fake successful health api
	//assert.NoError(awaitNetwork(net))
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = true
	}
	// TODO: needs to set fake successful info api
	checkNetwork(t, net, runningNodes, nil)
}

// TestNetworkNodeOps creates/waits/checks/stops a network created from an empty one
// nodes are first added one by one, then removed one by one. between all operations, a network check is performed
// the check verify that all the nodes api clients are up for started nodes, and down for removed nodes
// all nodes are taken from config file
func TestNetworkNodeOps(t *testing.T) {
	// TODO: needs to set fake successful info api for running nodes and failed info api for removed nodes
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
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
	// TODO: needs to set fake successful health api
	//assert.NoError(awaitNetwork(net))
	var removedClients []api.Client
	for _, nodeConfig := range networkConfig.NodeConfigs {
		node, err := net.GetNode(nodeConfig.Name)
		assert.NoError(err)
		client := node.GetAPIClient()
		removedClients = append(removedClients, client)
		err = net.RemoveNode(nodeConfig.Name)
		assert.NoError(err)
		delete(runningNodes, nodeConfig.Name)
		checkNetwork(t, net, runningNodes, removedClients)
	}
}

// TestNodeNotFound checks operations fail for unkown node
func TestNodeNotFound(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, network.Config{}, api.NewAPIClient, newMockProcessSuccessful)
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

// TestStoppedNetwork checks operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	assert := assert.New(t)
	networkConfig, err := GetNetworkConfig()
	assert.NoError(err)
	net, err := NewNetwork(logging.NoLog{}, network.Config{}, api.NewAPIClient, newMockProcessSuccessful)
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
	assert.EqualValues(awaitNetwork(net), errStopped)
}

func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]bool, removedClients []api.Client) {
	assert := assert.New(t)
	nodeNames, err := net.GetNodesNames()
	assert.NoError(err)
	assert.EqualValues(len(nodeNames), len(runningNodes))
	for nodeName := range runningNodes {
		_, err := net.GetNode(nodeName)
		assert.NoError(err)
		//client := node.GetAPIClient()
		//assert.NoError(client.InfoAPI().GetNodeID())
	}
	//for _, client := range removedClients {
	//	nodeID, err := client.InfoAPI().GetNodeID()
	//  assert.Error(err)
	//}
}

func awaitNetwork(net network.Network) error {
	healthyCh := net.Healthy()
	err, ok := <-healthyCh
	if ok {
		return err
	}
	return nil
}

func GetNetworkConfig() (network.Config, error) {
	networkConfig := network.Config{}
	genesisFile, err := os.ReadFile("test_files/network1/genesis.json")
	if err != nil {
		return networkConfig, err
	}
	cchainConfigFile, err := os.ReadFile("test_files/network1/cchain_config.json")
	if err != nil {
		return networkConfig, err
	}
	for i := 1; i <= 6; i++ {
		nodeConfig := node.Config{}
		nodeConfig.GenesisFile = genesisFile
		nodeConfig.CChainConfigFile = cchainConfigFile
		nodeConfig.Name = fmt.Sprintf("node%d", i)
		configFile, err := os.ReadFile(fmt.Sprintf("test_files/network1/node%d/config.json", i))
		if err != nil {
			return networkConfig, err
		}
		nodeConfig.ConfigFile = configFile
		certFile, err := os.ReadFile(fmt.Sprintf("test_files/network1/node%d/staking.crt", i))
		if err == nil {
			nodeConfig.StakingCert = certFile
		}
		keyFile, err := os.ReadFile(fmt.Sprintf("test_files/network1/node%d/staking.key", i))
		if err == nil {
			nodeConfig.StakingKey = keyFile
		}
		localNodeConf := &NodeConfig{}
		localNodeConf.BinaryPath = "pepito"
		localNodeConf.Stdout = os.Stdout
		localNodeConf.Stderr = os.Stderr
		nodeConfig.ImplSpecificConfig = *localNodeConf
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfig.NodeConfigs[0].IsBeacon = true
	return networkConfig, nil
}
