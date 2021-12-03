package k8s

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
)

// TestBuildNodeEnv tests the internal buildNodeEnv method which creates the env vars for the avalanche nodes
func TestBuildNodeEnv(t *testing.T) {
	genesis := defaultTestGenesis
	testConfig := `
	{
		"network-peer-list-gossip-frequency": "250ms",
		"network-max-reconnect-delay": "1s",
		"health-check-frequency": "2s"
	}`
	c := node.Config{
		ConfigFile: testConfig,
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
			Value: fmt.Sprint(defaultTestNetworkID),
		},
	}

	assert.ElementsMatch(t, envVars, controlVars)
}

// TestConvertKey tests the internal convertKey method which is used
// to convert from the avalanchego config file format to env vars
func TestConvertKey(t *testing.T) {
	testKey := "network-peer-list-gossip-frequency"
	controlKey := "AVAGO_NETWORK_PEER_LIST_GOSSIP_FREQUENCY"
	convertedKey := convertKey(testKey)
	assert.Equal(t, convertedKey, controlKey)
}

// TestCreateDeploymentConfig tests the internal createDeploymentFromConfig method which creates the k8s objects
func TestCreateDeploymentConfig(t *testing.T) {
	assert := assert.New(t)
	genesis := defaultTestGenesis

	nodeConfigs := []node.Config{
		{
			Name:        "test1",
			IsBeacon:    true,
			StakingKey:  "fooKey",
			StakingCert: "fooCert",
			ConfigFile:  "{}",
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
			StakingKey:  "barKey",
			StakingCert: "barCert",
			ConfigFile:  "{}",
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
	beacons, nonBeacons, err := createDeploymentFromConfig(genesis, nodeConfigs)
	assert.NoError(err)
	assert.Len(beacons, 1)
	assert.Len(nonBeacons, 1)

	b := beacons[0]
	n := nonBeacons[0]

	assert.Equal(b.Name, "test11")
	assert.Equal(n.Name, "test22")
	assert.Equal(b.Kind, "kinda")
	assert.Equal(n.Kind, "kindb")
	assert.Equal(b.APIVersion, "v1")
	assert.Equal(n.APIVersion, "v2")
	assert.Equal(b.Namespace, "test01")
	assert.Equal(n.Namespace, "test02")
	assert.Equal(b.Spec.DeploymentName, "test1")
	assert.Equal(n.Spec.DeploymentName, "test2")
	assert.Equal(b.Spec.Image, "img1")
	assert.Equal(n.Spec.Image, "img2")
	assert.Equal(b.Spec.Tag, "t1")
	assert.Equal(n.Spec.Tag, "t2")
	assert.Equal(b.Spec.BootstrapperURL, "")
	assert.Equal(n.Spec.BootstrapperURL, "")
	assert.Equal(b.Spec.Env[0].Name, "AVAGO_NETWORK_ID")
	assert.Equal(n.Spec.Env[0].Name, "AVAGO_NETWORK_ID")
	assert.Equal(b.Spec.Env[0].Value, fmt.Sprint(defaultTestNetworkID))
	assert.Equal(n.Spec.Env[0].Value, fmt.Sprint(defaultTestNetworkID))
	assert.Equal(b.Spec.NodeCount, 1)
	assert.Equal(n.Spec.NodeCount, 1)
	assert.Equal(b.Spec.Certificates[0].Cert, base64.StdEncoding.EncodeToString([]byte("fooCert")))
	assert.Equal(b.Spec.Certificates[0].Key, base64.StdEncoding.EncodeToString([]byte("fooKey")))
	assert.Equal(n.Spec.Certificates[0].Cert, base64.StdEncoding.EncodeToString([]byte("barCert")))
	assert.Equal(n.Spec.Certificates[0].Key, base64.StdEncoding.EncodeToString([]byte("barKey")))
	assert.Equal(n.Spec.NodeCount, 1)
	assert.Equal(b.Spec.Genesis, string(genesis))
	assert.Equal(n.Spec.Genesis, string(genesis))
}
