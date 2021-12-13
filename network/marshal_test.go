package network_test

import (
	"encoding/json"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

func TestMarshalling(t *testing.T) {
	jsonNetcfg := "{\"implSpecCfg\":\"\",\"genesis\":\"in the beginning there was a token\",\"nodeConfigs\":[{\"implSpecCfg\":{\"binaryPath\":\"/tmp/some/file/path\"},\"name\":\"node0\",\"isBeacon\":true,\"stakingKey\":\"key123\",\"stakingCert\":\"cert123\",\"confFile\":\"config-file-blablabla1\",\"cchainConfFile\":\"cchain-config-file-blablabla1\"},{\"implSpecCfg\":{\"apiVersion\":\"0.99.999\",\"identifier\":\"k8s-node-1\",\"image\":\"therepo/theimage\",\"kind\":\"imaginary\",\"namespace\":\"outer-space\",\"tag\":\"omega\"},\"name\":\"node1\",\"isBeacon\":false,\"stakingKey\":\"key456\",\"stakingCert\":\"cert456\",\"confFile\":\"config-file-blablabla2\",\"cchainConfFile\":\"cchain-config-file-blablabla2\"},{\"implSpecCfg\":{\"binaryPath\":\"/tmp/some/other/path\"},\"name\":\"node2\",\"isBeacon\":false,\"stakingKey\":\"key789\",\"stakingCert\":\"cert789\",\"confFile\":\"config-file-blablabla3\",\"cchainConfFile\":\"cchain-config-file-blablabla3\"}],\"logLevel\":\"DEBUG\",\"name\":\"abcxyz\"}"

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
			},
			{
				ImplSpecificConfig: newTestK8sNodeConfigJsonRaw(),
				Name:               "node1",
				IsBeacon:           false,
				StakingKey:         "key456",
				StakingCert:        "cert456",
				ConfigFile:         "config-file-blablabla2",
				CChainConfigFile:   "cchain-config-file-blablabla2",
			},
			{
				ImplSpecificConfig: utils.NewLocalNodeConfigJsonRaw("/tmp/some/other/path"),
				Name:               "node2",
				IsBeacon:           false,
				StakingKey:         "key789",
				StakingCert:        "cert789",
				ConfigFile:         "config-file-blablabla3",
				CChainConfigFile:   "cchain-config-file-blablabla3",
			},
		},
		LogLevel: "DEBUG",
		Name:     "abcxyz",
	}

	var netcfg network.Config
	if err := json.Unmarshal([]byte(jsonNetcfg), &netcfg); err != nil {
		t.Fatal(err)
	}

	assert := assert.New(t)
	assert.EqualValues(control, netcfg)

	// At this point unmarshalling should succeed because *the json.RawMessages are ignored*.
	// Let's try creating a local network first: it should fail as the second node is for k8s
	_, err := local.NewNetwork(logging.NoLog{}, netcfg)
	assert.Error(err)

	var localcfg local.NodeConfig
	err = json.Unmarshal([]byte(control.NodeConfigs[0].ImplSpecificConfig), &localcfg)
	assert.NoError(err)
	assert.NotEmpty(localcfg.BinaryPath)
	localcfg.BinaryPath = ""
	err = json.Unmarshal([]byte(control.NodeConfigs[2].ImplSpecificConfig), &localcfg)
	assert.NoError(err)
	assert.NotEmpty(localcfg.BinaryPath)
	localcfg.BinaryPath = ""
	err = json.Unmarshal([]byte(control.NodeConfigs[1].ImplSpecificConfig), &localcfg)
	assert.NoError(err)
	assert.Empty(localcfg.BinaryPath)

	var k8scfg k8s.ObjectSpec
	assert.Empty(k8scfg.APIVersion)
	err = json.Unmarshal([]byte(control.NodeConfigs[0].ImplSpecificConfig), &k8scfg)
	assert.NoError(err)
	assert.Empty(k8scfg.APIVersion)
	err = json.Unmarshal([]byte(control.NodeConfigs[2].ImplSpecificConfig), &k8scfg)
	assert.NoError(err)
	assert.Empty(k8scfg.APIVersion)
	err = json.Unmarshal([]byte(control.NodeConfigs[1].ImplSpecificConfig), &k8scfg)
	assert.NoError(err)
	assert.NotEmpty(k8scfg.APIVersion)
}

func newTestK8sNodeConfigJsonRaw() json.RawMessage {
	return utils.NewK8sNodeConfigJsonRaw("0.99.999", "k8s-node-1", "therepo/theimage", "imaginary", "outer-space", "omega")
}
