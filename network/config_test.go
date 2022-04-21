package network_test

import (
	"encoding/json"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/stretchr/testify/assert"
)

func TestConfigMarshalJSON(t *testing.T) {
	jsonNetcfg := "{\"implSpecificConfig\":\"\",\"genesis\":\"in the beginning there was a token\",\"nodeConfigs\":[{\"implSpecificConfig\":{\"binaryPath\":\"/tmp/some/file/path\"},\"name\":\"node0\",\"isBeacon\":true,\"stakingKey\":\"key123\",\"stakingCert\":\"cert123\",\"configFile\":\"config-file-blablabla1\",\"cchainConfigFile\":\"cchain-config-file-blablabla1\",\"flags\":{\"flag-one\":\"val-one\",\"flag-two\":2}},{\"implSpecificConfig\":{\"binaryPath\":\"/tmp/some/other/path\"},\"name\":\"node2\",\"isBeacon\":false,\"stakingKey\":\"key789\",\"stakingCert\":\"cert789\",\"configFile\":\"config-file-blablabla3\",\"cchainConfigFile\":\"cchain-config-file-blablabla3\",\"flags\":{\"flag-one\":\"val-one\",\"flag-two\":2}}],\"logLevel\":\"DEBUG\",\"name\":\"abcxyz\",\"flags\":{\"flag-three\":\"val-three\"}}"

	control := network.Config{
		Genesis: "in the beginning there was a token",
		NodeConfigs: []node.Config{
			{
				ImplSpecificConfig: utils.NewLocalNodeConfigJsonRaw("/tmp/some/file/path"),
				Name:               "node0",
				IsBeacon:           true,
				StakingKey:         "key123",
				StakingCert:        "cert123",
				ConfigFile:         "config-file-blablabla1",
				CChainConfigFile:   "cchain-config-file-blablabla1",
				Flags: map[string]interface{}{
					"flag-one": "val-one",
					"flag-two": float64(2),
				},
			},
			{
				ImplSpecificConfig: utils.NewLocalNodeConfigJsonRaw("/tmp/some/other/path"),
				Name:               "node2",
				IsBeacon:           false,
				StakingKey:         "key789",
				StakingCert:        "cert789",
				ConfigFile:         "config-file-blablabla3",
				CChainConfigFile:   "cchain-config-file-blablabla3",
				Flags: map[string]interface{}{
					"flag-one": "val-one",
					"flag-two": float64(2),
				},
			},
		},
		LogLevel: "DEBUG",
		Name:     "abcxyz",
		Flags: map[string]interface{}{
			"flag-three": "val-three",
		},
	}

	var netcfg network.Config
	if err := json.Unmarshal([]byte(jsonNetcfg), &netcfg); err != nil {
		t.Fatal(err)
	}

	assert := assert.New(t)
	assert.EqualValues(control, netcfg)

	var localcfg local.NodeConfig
    err := json.Unmarshal([]byte(control.NodeConfigs[0].ImplSpecificConfig), &localcfg)
	assert.NoError(err)
	assert.NotEmpty(localcfg.BinaryPath)
	localcfg.BinaryPath = ""
	err = json.Unmarshal([]byte(control.NodeConfigs[1].ImplSpecificConfig), &localcfg)
	assert.NoError(err)
	assert.NotEmpty(localcfg.BinaryPath)
}
