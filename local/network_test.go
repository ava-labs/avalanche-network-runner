package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/client"
	"github.com/ava-labs/avalanche-network-runner-local/local/mocks"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

var _ NewNodeProcessF = newFullMockProcess
var _ NewNodeProcessF = newMockProcessStartFail

func newFullMockProcess(node.Config, ...string) (NodeProcess, error) {
	process := &mocks.NodeProcess{}
	process.On("Start").Return(nil)
	process.On("Wait").Return(nil)
	process.On("Stop").Return(nil)
	return process, nil
}

func newMockProcessStartFail(node.Config, ...string) (NodeProcess, error) {
	process := &mocks.NodeProcess{}
	process.On("Start").Return(errors.New("Start failed"))
	process.On("Wait").Return(nil)
	process.On("Stop").Return(nil)
	return process, nil
}

func TestWrongNetworkConfigs(t *testing.T) {
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
		_, err := startNetwork(t, &networkConfig)
		assert.Error(t, err)
	}
}

func TestImplSpecificConfigInterface(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	networkConfig.NodeConfigs[0].ImplSpecificConfig = "pepito"
	_, err = startNetwork(t, networkConfig)
	if err == nil {
		t.Fatal(err)
	}
}

func TestInvalidCommand(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewNetwork(logging.NoLog{}, *networkConfig, client.NewAPIClient, newMockProcessStartFail)
	if err == nil {
		t.Fatal(err)
	}
}

/*
Needs fake health api?
func TestUnhealthyNetwork(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	networkConfig.NodeConfigs[0].ConfigFile = []byte(`{"network-id":1}`)
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	if awaitNetwork(net) == nil {
		t.Fatal(errors.New("expected network to never get healthy, but it was"))
	}
}
*/

func TestGeneratedNodesNames(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	for i := range networkConfig.NodeConfigs {
		networkConfig.NodeConfigs[i].Name = ""
	}
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	nodeNameMap := make(map[string]bool)
	nodeNames, err := net.GetNodesNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeName := range nodeNames {
		nodeNameMap[nodeName] = true
	}
	if len(nodeNameMap) != len(networkConfig.NodeConfigs) {
		t.Fatalf("number of unique node names in network %v differs from number of nodes in config %v", len(nodeNameMap), len(networkConfig.NodeConfigs))
	}
}

// TODO add byzantine node to conf
// TestNetworkFromConfig creates/waits/checks/stops a network from config file
// the check verify that all the nodes api clients are up
func TestNetworkFromConfig(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	if err := awaitNetwork(net); err != nil {
		t.Fatal(err)
	}
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = true
	}
	if err := checkNetwork(t, net, runningNodes, nil); err != nil {
		t.Fatal(err)
	}
}

// TestNetworkNodeOps creates/waits/checks/stops a network created from an empty one
// nodes are first added one by one, then removed one by one. between all operations, a network check is performed
// the check verify that all the nodes api clients are up for started nodes, and down for removed nodes
// all nodes are taken from config file
func TestNetworkNodeOps(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err = net.AddNode(nodeConfig)
		if err != nil {
			t.Fatal(err)
		}
		runningNodes[nodeConfig.Name] = true
		if err := checkNetwork(t, net, runningNodes, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitNetwork(net); err != nil {
		t.Fatal(err)
	}
	var removedClients []api.Client
	for _, nodeConfig := range networkConfig.NodeConfigs {
		node, err := net.GetNode(nodeConfig.Name)
		if err != nil {
			t.Fatal(err)
		}
		client := node.GetAPIClient()
		removedClients = append(removedClients, client)
		err = net.RemoveNode(nodeConfig.Name)
		if err != nil {
			t.Fatal(err)
		}
		delete(runningNodes, nodeConfig.Name)
		if err := checkNetwork(t, net, runningNodes, removedClients); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNodeNotFound checks operations fail for unkown node
func TestNodeNotFound(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	if err != nil {
		t.Fatal(err)
	}
	// get correct node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	// get uncorrect node (non created)
	_, err = net.GetNode(networkConfig.NodeConfigs[1].Name)
	if err == nil {
		t.Fatal(err)
	}
	// remove uncorrect node (non created)
	err = net.RemoveNode(networkConfig.NodeConfigs[1].Name)
	if err == nil {
		t.Fatal(err)
	}
	// remove correct node
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	// get uncorrect node (removed)
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err == nil {
		t.Fatal(err)
	}
	// remove uncorrect node (removed)
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err == nil {
		t.Fatal(err)
	}
	ctx := context.TODO()
	err = net.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

// TestStoppedNetwork checks operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	networkConfig, err := GetNetworkConfig()
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	if err != nil {
		t.Fatal(err)
	}
	// first GetNodesNames should return some nodes
	_, err = net.GetNodesNames()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.TODO()
	err = net.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Stop failure
	err = net.Stop(ctx)
	if err == nil {
		t.Fatal(err)
	}
	// AddNode failure
	_, err = net.AddNode(networkConfig.NodeConfigs[1])
	if err != errStopped {
		t.Fatal(err)
	}
	// GetNode failure
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err != errStopped {
		t.Fatal(err)
	}
	// second GetNodesNames should return no nodes
	_, err = net.GetNodesNames()
	if err != errStopped {
		t.Fatal(err)
	}
	// RemoveNode failure
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err != errStopped {
		t.Fatal(err)
	}
	// Healthy failure
	healthyCh := net.Healthy()
	err, ok := <-healthyCh
	if !ok {
		t.Fatal(errors.New("should return error"))
	}
	//err = awaitNetwork(net)
	if err != errStopped {
		t.Fatal(err)
	}
}

func startNetwork(t *testing.T, networkConfig *network.Config) (network.Network, error) {
	net, err := NewNetwork(logging.NoLog{}, *networkConfig, client.NewAPIClient, newFullMockProcess)
	if err != nil {
		return nil, err
	}
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	go func() {
		<-signalsCh
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	return net, nil
}

func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]bool, removedClients []api.Client) error {
	nodeNames, err := net.GetNodesNames()
	if err != nil {
		return err
	}
	if len(nodeNames) != len(runningNodes) {
		return fmt.Errorf("GetNodesNames() len %v should equal number of running nodes %v", len(nodeNames), len(runningNodes))
	}
	for nodeName := range runningNodes {
		_, err := net.GetNode(nodeName)
		if err != nil {
			return err
		}
		//client := node.GetAPIClient()
		//if _, err := client.InfoAPI().GetNodeID(); err != nil {
		//	return err
		//}
	}
	//for _, client := range removedClients {
	//	nodeID, err := client.InfoAPI().GetNodeID()
	//	if err == nil {
	//		return fmt.Errorf("removed node %v is answering requests", nodeID)
	//	}
	//}
	return nil
}

func awaitNetwork(net network.Network) error {
	/*
		timeoutCh := make(chan struct{})
		go func() {
			time.Sleep(5 * time.Minute)
			timeoutCh <- struct{}{}
		}()
		healthyCh := net.Healthy()
		select {
		case err, ok := <-healthyCh:
			if ok {
				ctx := context.TODO()
				_ = net.Stop(ctx)
				return err
			}
		case <-timeoutCh:
			ctx := context.TODO()
			_ = net.Stop(ctx)
			return errors.New("network startup timeout")
		}
	*/
	return nil
}

func GetNetworkConfig() (*network.Config, error) {
	genesisFile, err := os.ReadFile("test_files/network1/genesis.json")
	if err != nil {
		return nil, err
	}
	cchainConfigFile, err := os.ReadFile("test_files/network1/cchain_config.json")
	if err != nil {
		return nil, err
	}
	networkConfig := network.Config{}
	for i := 1; i <= 6; i++ {
		nodeConfig := node.Config{}
		nodeConfig.GenesisFile = genesisFile
		nodeConfig.CChainConfigFile = cchainConfigFile
		nodeConfig.Name = fmt.Sprintf("node%d", i)
		configFile, err := os.ReadFile(fmt.Sprintf("test_files/network1/node%d/config.json", i))
		if err != nil {
			return nil, err
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
	return &networkConfig, nil
}

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
		client.NewAPIClient, // TODO change AvalancheGo so we can mock API clients
		newMockProcess,
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
		client.NewAPIClient,
		newProcessF,
	)
	assert.NoError(err)
	// Assert that GetNodesNames() includes only the 1 node's name
	names, err := net.GetNodesNames()
	assert.NoError(err)
	assert.Contains(names, nodeName)
	assert.Len(names, 1)
}
