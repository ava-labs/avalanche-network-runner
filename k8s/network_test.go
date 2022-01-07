package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	apimocks "github.com/ava-labs/avalanche-network-runner/api/mocks"
	"github.com/ava-labs/avalanche-network-runner/k8s/mocks"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"

	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	k8scli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultTestNetworkID   = uint32(1337)
	defaultTestNetworkSize = 5
)

var (
	_                  api.NewAPIClientF = newMockAPISuccessful
	_                  api.NewAPIClientF = newMockAPIUnhealthy
	defaultTestGenesis []byte            = []byte(
		`{
			"networkID": 1337,
			"allocations": [
			  {
				"ethAddr": "0xb3d82b1367d362de99ab59a658165aff520cbd4d",
				"avaxAddr": "X-local1g65uqn6t77p656w64023nh8nd9updzmxyymev2",
				"initialAmount": 0,
				"unlockSchedule": [
				  {
					"amount": 10000000000000000,
					"locktime": 1633824000
				  }
				]
			  },
			  {
				"ethAddr": "0xb3d82b1367d362de99ab59a658165aff520cbd4d",
				"avaxAddr": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"initialAmount": 300000000000000000,
				"unlockSchedule": [
				  {
					"amount": 20000000000000000
				  },
				  {
					"amount": 10000000000000000,
					"locktime": 1633824000
				  }
				]
			  },
			  {
				"ethAddr": "0xb3d82b1367d362de99ab59a658165aff520cbd4d",
				"avaxAddr": "X-custom1ur873jhz9qnaqv5qthk5sn3e8nj3e0kmzpjrhp",
				"initialAmount": 10000000000000000,
				"unlockSchedule": [
				  {
					"amount": 10000000000000000,
					"locktime": 1633824000
				  }
				]
			  }
			],
			"startTime": 1630987200,
			"initialStakeDuration": 31536000,
			"initialStakeDurationOffset": 5400,
			"initialStakedFunds": [
			  "X-custom1g65uqn6t77p656w64023nh8nd9updzmxwd59gh"
			],
			"initialStakers": [
			  {
				"nodeID": "NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg",
				"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"delegationFee": 1000000
			  },
			  {
				"nodeID": "NodeID-MFrZFVCXPv5iCn6M9K6XduxGTYp891xXZ",
				"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"delegationFee": 500000
			  },
			  {
				"nodeID": "NodeID-NFBbbJ4qCmNaCzeW7sxErhvWqvEQMnYcN",
				"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"delegationFee": 250000
			  },
			  {
				"nodeID": "NodeID-GWPcbFJZFfZreETSoWjPimr846mXEKCtu",
				"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"delegationFee": 125000
			  },
			  {
				"nodeID": "NodeID-P7oB2McjBGgW2NXXWVYjV8JEDFoW9xDE5",
				"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
				"delegationFee": 62500
			  }
			],
			"cChainGenesis": "{\"config\":{\"chainId\":43112,\"homesteadBlock\":0,\"daoForkBlock\":0,\"daoForkSupport\":true,\"eip150Block\":0,\"eip150Hash\":\"0x2086799aeebeae135c246c65021c82b4e15a2c451340993aacfd2751886514f0\",\"eip155Block\":0,\"eip158Block\":0,\"byzantiumBlock\":0,\"constantinopleBlock\":0,\"petersburgBlock\":0,\"istanbulBlock\":0,\"muirGlacierBlock\":0,\"apricotPhase1BlockTimestamp\":0,\"apricotPhase2BlockTimestamp\":0},\"nonce\":\"0x0\",\"timestamp\":\"0x0\",\"extraData\":\"0x00\",\"gasLimit\":\"0x5f5e100\",\"difficulty\":\"0x0\",\"mixHash\":\"0x0000000000000000000000000000000000000000000000000000000000000000\",\"coinbase\":\"0x0000000000000000000000000000000000000000\",\"alloc\":{\"8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC\":{\"balance\":\"0x295BE96E64066972000000\"}},\"number\":\"0x0\",\"gasUsed\":\"0x0\",\"parentHash\":\"0x0000000000000000000000000000000000000000000000000000000000000000\"}",
			"message": "{{ fun_quote }}"
		  }
		  `,
	)
)

// newMockK8sClient creates a new mock client
func newMockK8sClient() k8scli.Client {
	client := &mocks.Client{}
	client.On("Get", mock.Anything, mock.Anything, mock.Anything).Run(
		func(args mock.Arguments) {
			arg := args.Get(2).(*k8sapi.Avalanchego)
			arg.Status.NetworkMembersURI = []string{"localhost"}
		}).Return(nil)
	client.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	client.On("DeleteAllOf", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	client.On("Create", mock.Anything, mock.Anything).Return(nil)
	client.On("Status").Return(nil)
	client.On("Scheme").Return(nil)
	client.On("RESTMapper").Return(nil)
	return client
}

// newDNSChecker creates a mock for checking the DNS (really just a http.Get mock)
func newDNSChecker() *mocks.DnsReachableChecker {
	dnsChecker := &mocks.DnsReachableChecker{}
	dnsChecker.On("Reachable", mock.Anything, mock.AnythingOfType("string")).Return(true)
	return dnsChecker
}

// Returns an API client where:
// * The Health API's Health method always returns healthy
// * The CChainEthAPI's Close method may be called
// * Only the above 2 methods may be called
// TODO have this method return an API Client that has all
// APIs and methods implemented
func newMockAPISuccessful(ipAddr string, port uint16, requestTimeout time.Duration) api.Client {
	healthReply := &health.APIHealthClientReply{Healthy: true}
	healthClient := &apimocks.HealthClient{}
	healthClient.On("Health").Return(healthReply, nil)

	id := ids.GenerateTestShortID().String()
	infoReply := fmt.Sprintf("%s%s", constants.NodeIDPrefix, id)
	infoClient := &apimocks.InfoClient{}
	infoClient.On("GetNodeID").Return(infoReply, nil)

	client := &apimocks.Client{}
	client.On("HealthAPI").Return(healthClient)
	client.On("InfoAPI").Return(infoClient)
	return client
}

// Returns an API client where the Health API's Health method always returns unhealthy
func newMockAPIUnhealthy(ipAddr string, port uint16, requestTimeout time.Duration) api.Client {
	healthReply := &health.APIHealthClientReply{Healthy: false}
	healthClient := &apimocks.HealthClient{}
	healthClient.On("Health").Return(healthReply, nil)
	client := &apimocks.Client{}
	client.On("HealthAPI").Return(healthClient)
	return client
}

func newDefaultTestNetwork(t *testing.T) (network.Network, error) {
	conf := defaultTestNetworkConfig(t)
	return newTestNetworkWithConfig(conf)
}

func newTestNetworkWithConfig(conf network.Config) (network.Network, error) {
	return newNetwork(networkParams{
		conf:          conf,
		log:           logging.NoLog{},
		k8sClient:     newMockK8sClient(),
		dnsChecker:    newDNSChecker(),
		apiClientFunc: newMockAPISuccessful,
	})
}

// cleanup closes the channel to shutdown the HTTP server
func cleanup(n network.Network) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := n.Stop(ctx); err != nil {
		fmt.Printf("Error stopping network: %s\n", err)
	}
}

// TestNewNetworkEmpty tests that an empty config results in an error
func TestNewNetworkEmpty(t *testing.T) {
	conf := network.Config{}
	_, err := newTestNetworkWithConfig(conf)
	assert.Error(t, err)
}

// TestHealthy tests that a default network can be created and becomes healthy
func TestHealthy(t *testing.T) {
	n, err := newDefaultTestNetwork(t)
	assert.NoError(t, err)
	defer cleanup(n)
	err = awaitNetworkHealthy(n, 30*time.Second)
	assert.NoError(t, err)
}

// TestNetworkDefault tests the default operations on a network:
// * it creates a network and waits until it's healthy
// * it adds a new node
// * it gets a single node
// * it gets all nodes
// * it removes a node
// * it stops the network
func TestNetworkDefault(t *testing.T) {
	assert := assert.New(t)
	conf := defaultTestNetworkConfig(t)
	n, err := newTestNetworkWithConfig(conf)
	assert.NoError(err)
	defer cleanup(n)
	err = awaitNetworkHealthy(n, 30*time.Second)
	assert.NoError(err)

	net, ok := n.(*networkImpl)
	assert.True(ok)
	assert.Len(net.nodes, len(conf.NodeConfigs))
	for _, node := range net.nodes {
		assert.NotNil(node.apiClient)
		assert.NotNil(node.k8sObjSpec)
		assert.True(len(node.name) > 0)
		assert.True(len(node.uri) > 0)
		assert.NotEqualValues(ids.ShortEmpty, node.nodeID)
	}

	names, err := n.GetNodeNames()
	assert.NoError(err)
	netSize := len(names)
	assert.EqualValues(defaultTestNetworkSize, netSize)
	for _, name := range names {
		assert.Greater(len(name), 0)
	}
	stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
	assert.NoError(err)

	newNodeConfig := node.Config{
		Name:        "new-node",
		IsBeacon:    false,
		StakingKey:  string(stakingKey),
		StakingCert: string(stakingCert),
		ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw(
			"chain.avax.network/v1alpha1",
			"new-node",
			"avaplatform/avalanchego",
			"Avalanchego",
			"ci-network-runner",
			"9.99.9999",
		),
	}
	newNode, err := n.AddNode(newNodeConfig)
	assert.NoError(err)
	names, err = n.GetNodeNames()
	assert.NoError(err)
	assert.Len(names, netSize+1)

	nn, err := n.GetNode(newNodeConfig.Name)
	assert.NoError(err)
	assert.Equal(newNodeConfig.Name, nn.GetName())

	_, err = n.GetNode("this does not exist")
	assert.Error(err)

	err = n.RemoveNode(newNode.GetName())
	assert.NoError(err)
	names, err = n.GetNodeNames()
	assert.NoError(err)
	assert.Len(names, netSize)
}

// TestWrongNetworkConfigs checks configs that are expected to be invalid at network creation time
// This is adapted from the local test suite
func TestWrongNetworkConfigs(t *testing.T) {
	tests := map[string]struct {
		config network.Config
	}{
		"no ImplSpecificConfig": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						IsBeacon:    true,
						StakingKey:  "nonempty",
						StakingCert: "nonempty",
					},
				},
			},
		},
		"empty name": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						IsBeacon:           true,
						StakingKey:         "nonempty",
						StakingCert:        "nonempty",
					},
				},
			},
		},
		"StakingKey but no StakingCert": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						IsBeacon:           true,
						StakingKey:         "nonempty",
					},
				},
			},
		},
		"StakingCert but no StakingKey": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						IsBeacon:           true,
						StakingCert:        "nonempty",
					},
				},
			},
		},
		"no beacon node": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						StakingKey:         "nonempty",
						StakingCert:        "nonempty",
					},
				},
			},
		},
		"repeated name": {
			config: network.Config{
				Genesis: "nonempty",
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						Name:               "node0",
						IsBeacon:           true,
						StakingKey:         "nonempty",
						StakingCert:        "nonempty",
					},
					{
						ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", "testnode", "somerepo/someimage", "anykind", "noname", "testingversion"),
						Name:               "node0",
						IsBeacon:           false,
						StakingKey:         "nonempty",
						StakingCert:        "nonempty",
					},
				},
			},
		},
	}
	assert := assert.New(t)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := newTestNetworkWithConfig(tt.config)
			assert.Error(err)
		})
	}
}

// TestFlags tests that we can pass flags through the network.Config
// but also via node.Config and that the latter overrides the former
// if same keys exist.
func TestFlags(t *testing.T) {
	assert := assert.New(t)
	networkConfig := defaultTestNetworkConfig(t)

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
	nw, err := newTestNetworkWithConfig(networkConfig)
	assert.NoError(err)
	// after creating the network, one flag should have been overridden by the node configs
	for _, n := range networkConfig.NodeConfigs {
		assert.Len(n.Flags, 4)
		assert.Contains(n.Flags, "test-network-config-flag")
		assert.Equal(n.Flags["test-network-config-flag"], "something")
		assert.Contains(n.Flags, "common-config-flag")
		assert.Equal(n.Flags["common-config-flag"], "this should be added")
		assert.Contains(n.Flags, "test-node-config-flag")
		assert.Equal(n.Flags["test-node-config-flag"], "node")
		assert.Contains(n.Flags, "test2-node-config-flag")
		assert.Equal(n.Flags["test2-node-config-flag"], "config")
	}
	err = nw.Stop(context.Background())
	assert.NoError(err)

	// submit only node.Config flags
	networkConfig.Flags = nil
	for i := range networkConfig.NodeConfigs {
		v := &networkConfig.NodeConfigs[i]
		v.Flags = map[string]interface{}{
			"test-node-config-flag":  "node",
			"test2-node-config-flag": "config",
			"common-config-flag":     "this should be added",
		}
	}
	nw, err = newTestNetworkWithConfig(networkConfig)
	assert.NoError(err)
	// after creating the network, only node configs should exist
	for _, n := range networkConfig.NodeConfigs {
		assert.Len(n.Flags, 3)
		assert.NotContains(n.Flags, "test-network-config-flag")
		assert.Contains(n.Flags, "common-config-flag")
		assert.Equal(n.Flags["common-config-flag"], "this should be added")
		assert.Contains(n.Flags, "test-node-config-flag")
		assert.Equal(n.Flags["test-node-config-flag"], "node")
		assert.Contains(n.Flags, "test2-node-config-flag")
		assert.Equal(n.Flags["test2-node-config-flag"], "config")
	}
	err = nw.Stop(context.Background())
	assert.NoError(err)

	// submit only network.Config flags
	networkConfig.Flags = map[string]interface{}{
		"test-network-config-flag": "something",
		"common-config-flag":       "else",
	}
	for i := range networkConfig.NodeConfigs {
		v := &networkConfig.NodeConfigs[i]
		v.Flags = nil
	}
	nw, err = newTestNetworkWithConfig(networkConfig)
	assert.NoError(err)
	// after creating the network, only flags from the network config should exist
	for _, n := range networkConfig.NodeConfigs {
		assert.True(len(n.Flags) == 2)
		assert.Contains(n.Flags, "test-network-config-flag")
		assert.Equal(n.Flags["test-network-config-flag"], "something")
		assert.Contains(n.Flags, "common-config-flag")
		assert.Equal(n.Flags["common-config-flag"], "else")
	}
	err = nw.Stop(context.Background())
	assert.NoError(err)
}

// TestImplSpecificConfigInterface checks incorrect type to interface{} ImplSpecificConfig
// This is adapted from the local test suite
func TestImplSpecificConfigInterface(t *testing.T) {
	assert := assert.New(t)
	networkConfig := defaultTestNetworkConfig(t)
	networkConfig.NodeConfigs[0].ImplSpecificConfig = json.RawMessage("should be a JSON")
	_, err := newTestNetworkWithConfig(networkConfig)
	assert.Error(err)
}

// defaultTestNetworkConfig creates a default size network for testing
func defaultTestNetworkConfig(t *testing.T) network.Config {
	assert := assert.New(t)
	networkConfig := network.Config{}
	for i := 0; i < defaultTestNetworkSize; i++ {
		crt, key, err := staking.NewCertAndKeyBytes()
		assert.NoError(err)
		nodeConfig := node.Config{
			Name:               fmt.Sprintf("node%d", i),
			ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw("0.00.0000", fmt.Sprintf("testnode-%d", i), "somerepo/someimage", "Avalanchego", "ci-networkrunner", "testingversion"),
			StakingKey:         string(key),
			StakingCert:        string(crt),
		}
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfig.NodeConfigs[0].IsBeacon = true
	return networkConfig
}

// Returns nil when all the nodes in [net] are healthy,
// or an error if one doesn't become healthy within
// the timeout.
func awaitNetworkHealthy(net network.Network, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	healthyCh := net.Healthy(ctx)
	return <-healthyCh
}
