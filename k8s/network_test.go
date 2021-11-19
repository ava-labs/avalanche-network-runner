package k8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/constants"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
	v1 "k8s.io/api/core/v1"

	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"

	"github.com/stretchr/testify/assert"
	k8scli "sigs.k8s.io/controller-runtime/pkg/client"
)

// newFakeK8sClient creates a new fake (mock) client
func newFakeK8sClient() (k8scli.Client, error) {
	return newFakeOperatorClient()
}

// cleanup closes the channel to shutdown the HTTP server
func cleanup(n network.Network) {
	nn := n.(*networkImpl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.Stop(ctx); err != nil {
		fmt.Printf("Error stopping network: %s\n", err)
	}
	cli := nn.k8scli.(*fakeOperatorClient)
	cli.Close()
}

// TestNewNetworkEmpty tests that an empty config results in an error
func TestNewNetworkEmpty(t *testing.T) {
	conf := network.Config{}

	_, err := newNetwork(conf, logging.NoLog{}, newFakeK8sClient)
	assert.Error(t, err)
}

// TestNewNetwork tests that a default network can be created
func TestNewNetwork(t *testing.T) {
	conf := defaultNetworkConfig(t)

	n, err := newNetwork(conf, logging.NoLog{}, newFakeK8sClient)
	defer cleanup(n)
	assert.NoError(t, err)
}

// TestHealthy tests that a default network can be created and becomes healthy
func TestHealthy(t *testing.T) {
	conf := defaultNetworkConfig(t)

	n, err := awaitHealthy(t, conf)
	defer cleanup(n)
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
	conf := defaultNetworkConfig(t)

	n, err := awaitHealthy(t, conf)
	defer cleanup(n)
	assert.NoError(t, err)

	names, err := n.GetNodesNames()
	assert.NoError(t, err)
	netSize := len(names)
	if netSize != constants.DefaultNetworkSize {
		t.Fatalf("Expected default network size of %d, but got %d", constants.DefaultNetworkSize, netSize)
	}
	for _, name := range names {
		if name == "" {
			t.Fatal("Got invalid empty name")
		}
	}
	stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
	assert.NoError(t, err)

	nnode := node.Config{
		Name:        "new-node",
		IsBeacon:    false,
		StakingKey:  stakingKey,
		StakingCert: stakingCert,
		ImplSpecificConfig: ObjectSpec{
			Namespace:  "dev",
			Kind:       "Avalanchego",
			APIVersion: "chain.avax.network/v1alpha1",
			Identifier: "new-node",
			Image:      "avaplatform/avalanchego",
			Tag:        "1.9.99",
		},
	}
	newNode, err := n.AddNode(nnode)
	assert.NoError(t, err)
	names, err = n.GetNodesNames()
	assert.NoError(t, err)
	if len(names) != netSize+1 {
		t.Fatalf("Expected net size of %d, but got %d", netSize+1, len(names))
	}

	nn, err := n.GetNode(nnode.Name)
	assert.NoError(t, err)
	if nn.GetName() != nnode.Name {
		t.Fatalf("Expected node %s, but got %s", nn.GetName(), nnode.Name)
	}

	_, err = n.GetNode("this does not exist")
	assert.Error(t, err)

	err = n.RemoveNode(newNode.GetName())
	assert.NoError(t, err)
	names, err = n.GetNodesNames()
	assert.NoError(t, err)
	if len(names) != netSize {
		t.Fatalf("Expected net size of %d, but got %d", netSize, len(names))
	}
}

// TestWrongNetworkConfigs checks configs that are expected to be invalid at network creation time
// This is adapted from the local test suite
func TestWrongNetworkConfigs(t *testing.T) {
	tests := map[string]struct {
		config network.Config
	}{
		"no ImplSpecificConfig": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						IsBeacon:    true,
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
		"empty nodeID": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.ShortEmpty,
						},
						IsBeacon:    true,
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
		"no Genesis": {
			config: network.Config{
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						IsBeacon:    true,
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
		"StakingKey but no StakingCert": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						IsBeacon:   true,
						StakingKey: []byte("nonempty"),
					},
				},
			},
		},
		"StakingCert but no StakingKey": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						IsBeacon:    true,
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
		"no beacon node": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
		"repeated name": {
			config: network.Config{
				Genesis: []byte("nonempty"),
				NodeConfigs: []node.Config{
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						Name:        "node0",
						IsBeacon:    true,
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
					{
						ImplSpecificConfig: Node{
							nodeID: ids.GenerateTestShortID(),
						},
						Name:        "node0",
						IsBeacon:    true,
						StakingKey:  []byte("nonempty"),
						StakingCert: []byte("nonempty"),
					},
				},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			_, err := newNetwork(tt.config, logging.NoLog{}, newFakeK8sClient)
			assert.Error(err)
		})
	}
}

// TestImplSpecificConfigInterface checks incorrect type to interface{} ImplSpecificConfig
// This is adapted from the local test suite
func TestImplSpecificConfigInterface(t *testing.T) {
	assert := assert.New(t)
	networkConfig := defaultNetworkConfig(t)
	networkConfig.NodeConfigs[0].ImplSpecificConfig = "should not be string"
	_, err := newNetwork(networkConfig, logging.NoLog{}, newFakeK8sClient)
	assert.Error(err)
}

// TestCreateDeploymentConfig tests the internal createDeploymentFromConfig method which creates the k8s objects
func TestCreateDeploymentConfig(t *testing.T) {
	genesis, err := os.ReadFile("genesis_test.json")
	assert.NoError(t, err)

	nodeConfigs := []node.Config{
		{
			Name:        "test1",
			IsBeacon:    true,
			StakingKey:  []byte("fooKey"),
			StakingCert: []byte("fooCert"),
			ConfigFile:  []byte("{}"),
			ImplSpecificConfig: ObjectSpec{
				Namespace:  "test01",
				Identifier: "test11",
				Kind:       "kinda",
				APIVersion: "v1",
				Image:      "img1",
				Tag:        "t1",
				Genesis:    "gen1",
			},
		},
		{
			Name:        "test2",
			IsBeacon:    false,
			StakingKey:  []byte("barKey"),
			StakingCert: []byte("barCert"),
			ConfigFile:  []byte("{}"),
			ImplSpecificConfig: ObjectSpec{
				Namespace:  "test02",
				Identifier: "test22",
				Kind:       "kindb",
				APIVersion: "v2",
				Image:      "img2",
				Tag:        "t2",
				Genesis:    "gen2",
			},
		},
	}
	beacons, non, err := createDeploymentFromConfig(genesis, nodeConfigs)
	assert.NoError(t, err)
	if len(beacons) != 1 {
		t.Fatalf("Expected 1 beacon, got %d", len(beacons))
	}
	if len(non) != 1 {
		t.Fatalf("Expected 1 non-beacon, got %d", len(non))
	}

	b := beacons[0]
	n := non[0]

	assert.Equal(t, b.Name, "test11")
	assert.Equal(t, n.Name, "test22")
	assert.Equal(t, b.Kind, "kinda")
	assert.Equal(t, n.Kind, "kindb")
	assert.Equal(t, b.APIVersion, "v1")
	assert.Equal(t, n.APIVersion, "v2")
	assert.Equal(t, b.Namespace, "test01")
	assert.Equal(t, n.Namespace, "test02")
	assert.Equal(t, b.Spec.DeploymentName, "test1")
	assert.Equal(t, n.Spec.DeploymentName, "test2")
	assert.Equal(t, b.Spec.Image, "img1")
	assert.Equal(t, n.Spec.Image, "img2")
	assert.Equal(t, b.Spec.Tag, "t1")
	assert.Equal(t, n.Spec.Tag, "t2")
	assert.Equal(t, b.Spec.BootstrapperURL, "")
	assert.Equal(t, n.Spec.BootstrapperURL, "")
	assert.Equal(t, b.Spec.Env[0].Name, "AVAGO_NETWORK_ID")
	assert.Equal(t, n.Spec.Env[0].Name, "AVAGO_NETWORK_ID")
	assert.Equal(t, b.Spec.Env[0].Value, fmt.Sprint(constants.DefaultNetworkID))
	assert.Equal(t, n.Spec.Env[0].Value, fmt.Sprint(constants.DefaultNetworkID))
	assert.Equal(t, b.Spec.NodeCount, 1)
	assert.Equal(t, n.Spec.NodeCount, 1)
	assert.Equal(t, b.Spec.Certificates[0].Cert, base64.StdEncoding.EncodeToString([]byte("fooCert")))
	assert.Equal(t, b.Spec.Certificates[0].Key, base64.StdEncoding.EncodeToString([]byte("fooKey")))
	assert.Equal(t, n.Spec.Certificates[0].Cert, base64.StdEncoding.EncodeToString([]byte("barCert")))
	assert.Equal(t, n.Spec.Certificates[0].Key, base64.StdEncoding.EncodeToString([]byte("barKey")))
	assert.Equal(t, n.Spec.NodeCount, 1)
	assert.Equal(t, b.Spec.Genesis, string(genesis))
	assert.Equal(t, n.Spec.Genesis, string(genesis))
}

// TestBuildNodeEnv tests the internal buildNodeEnv method which creates the env vars for the avalanche nodes
func TestBuildNodeEnv(t *testing.T) {
	genesis, err := os.ReadFile("genesis_test.json")
	assert.NoError(t, err)
	testConfig := `
	{
		"network-peer-list-gossip-frequency": "250ms",
		"network-max-reconnect-delay": "1s",
		"health-check-frequency": "2s"
	}`
	c := node.Config{
		ConfigFile: []byte(testConfig),
	}

	envVars, err := buildNodeEnv(genesis, c)
	assert.NoError(t, err)
	controlVars := []v1.EnvVar{
		{
			Name:  "AVAGO_NETWORK_PEER_LIST_GOSSIP_FREQUENCY",
			Value: "250ms",
		},
		{
			Name:  "AVAGO_NETWORK_MAX_RECONNECT_DELAY",
			Value: "1s",
		},
		{
			Name:  "AVAGO_HEALTH_CHECK_FREQUENCY",
			Value: "2s",
		},
		{
			Name:  "AVAGO_NETWORK_ID",
			Value: fmt.Sprint(constants.DefaultNetworkID),
		},
	}

	assert.ElementsMatch(t, envVars, controlVars)
}

// TestBuildNodeMapping tests the internal buildNodeMapping which acts as mapping between
// the user facing interface and the k8s world
func TestBuildNodeMapping(t *testing.T) {
	f, err := newFakeOperatorClient()
	assert.NoError(t, err)
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		select {
		default:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		_, err := http.Get("http://localhost:9650")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	net := &networkImpl{
		log:   logging.NoLog{},
		nodes: make(map[string]*Node),
	}
	controlSet := make([]*k8sapi.Avalanchego, constants.DefaultNetworkSize)
	for i := 0; i < constants.DefaultNetworkSize; i++ {
		name := "localhost"
		avago := &k8sapi.Avalanchego{
			Status: k8sapi.AvalanchegoStatus{
				NetworkMembersURI: []string{name},
			},
			Spec: k8sapi.AvalanchegoSpec{
				DeploymentName: name,
			},
		}
		controlSet[i] = avago
	}

	err = net.buildNodeMapping(controlSet)
	assert.NoError(t, err)

	i := 0
	for k, v := range net.nodes {
		assert.Equal(t, controlSet[i], v.k8sObj)
		assert.Equal(t, k, v.name)
		assert.Equal(t, v.uri, "localhost")
		assert.NotNil(t, v.client)
		assert.NotEqual(t, ids.ShortEmpty, v.nodeID)
	}
}

// TestConvertKey tests the internal convertKey method which is used
// to convert from the avalanchego config file format to env vars
func TestConvertKey(t *testing.T) {
	testKey := "network-peer-list-gossip-frequency"
	controlKey := "AVAGO_NETWORK_PEER_LIST_GOSSIP_FREQUENCY"
	convertedKey := convertKey(testKey)
	assert.Equal(t, convertedKey, controlKey)
}

// TestExtractNetworkID tests the internal getNetworkID method which
// extracts the NetworkID from the genesis file
func TestExtractNetworkID(t *testing.T) {
	genesis, err := os.ReadFile("genesis_test.json")
	assert.NoError(t, err)
	netID, err := getNetworkID(genesis)
	assert.NoError(t, err)
	assert.Equal(t, netID, float64(constants.DefaultNetworkID))
}

// awaitHealthy creates a new network from a config and waits until it's healthy
func awaitHealthy(t *testing.T, conf network.Config) (network.Network, error) {
	n, err := newNetwork(conf, logging.NoLog{}, newFakeK8sClient)
	assert.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	errCh := n.Healthy(ctx)
	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
		return n, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// defaultNetworkConfig creates a default size network for testing
func defaultNetworkConfig(t *testing.T) network.Config {
	assert := assert.New(t)
	networkConfig := network.Config{}
	for i := 0; i < constants.DefaultNetworkSize; i++ {
		crt, key, err := staking.NewCertAndKeyBytes()
		assert.NoError(err)
		nodeConfig := node.Config{
			Name: fmt.Sprintf("node%d", i),
			ImplSpecificConfig: ObjectSpec{
				Identifier: fmt.Sprintf("test-node-%d", i),
				Namespace:  "dev",
			},
			StakingKey:  key,
			StakingCert: crt,
		}
		networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
	}
	networkConfig.NodeConfigs[0].IsBeacon = true
	return networkConfig
}
