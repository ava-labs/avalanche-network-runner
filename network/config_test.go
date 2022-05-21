package network_test

import (
	"encoding/json"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/stretchr/testify/assert"
)

func TestConfigMarshalJSON(t *testing.T) {
	jsonNetcfg := "{\"genesis\":\"in the beginning there was a token\",\"nodeConfigs\":[{\"binaryPath\":\"/tmp/some/file/path\",\"name\":\"node1\",\"isBeacon\":true,\"stakingKey\":\"key123\",\"stakingCert\":\"cert123\",\"configFile\":\"config-file-blablabla1\",\"cchainConfigFile\":\"cchain-config-file-blablabla1\",\"flags\":{\"flag-one\":\"val-one\",\"flag-two\":2}},{\"binaryPath\":\"/tmp/some/other/path\",\"name\":\"node2\",\"isBeacon\":false,\"stakingKey\":\"key789\",\"stakingCert\":\"cert789\",\"configFile\":\"config-file-blablabla3\",\"cchainConfigFile\":\"cchain-config-file-blablabla3\",\"flags\":{\"flag-one\":\"val-one\",\"flag-two\":2}}],\"logLevel\":\"DEBUG\",\"name\":\"abcxyz\",\"flags\":{\"flag-three\":\"val-three\"}}"

	control := network.Config{
		Genesis: "in the beginning there was a token",
		NodeConfigs: []node.Config{
			{
				Name:             "node1",
				IsBeacon:         true,
				StakingKey:       "key123",
				StakingCert:      "cert123",
				ConfigFile:       "config-file-blablabla1",
				CChainConfigFile: "cchain-config-file-blablabla1",
				Flags: map[string]interface{}{
					"flag-one": "val-one",
					"flag-two": float64(2),
				},
				BinaryPath: "/tmp/some/file/path",
			},
			{
				Name:             "node2",
				IsBeacon:         false,
				StakingKey:       "key789",
				StakingCert:      "cert789",
				ConfigFile:       "config-file-blablabla3",
				CChainConfigFile: "cchain-config-file-blablabla3",
				Flags: map[string]interface{}{
					"flag-one": "val-one",
					"flag-two": float64(2),
				},
				BinaryPath: "/tmp/some/other/path",
			},
		},
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
}
