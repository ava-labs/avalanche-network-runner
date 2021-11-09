package local

import (
	_ "embed"
	"os"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/local/mocks"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

var _ NewNodeProcessF = newMockProcess

func newMockProcess(node.Config, ...string) (NodeProcess, error) {
	return &mocks.NodeProcess{}, nil
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
		newMockProcess,
	)
	assert.NoError(err)
	// Assert that GetNodesNames() includes only the 1 node's name
	names := net.GetNodesNames()
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
	names := net.GetNodesNames()
	assert.Contains(names, nodeName)
	assert.Len(names, 1)
}
