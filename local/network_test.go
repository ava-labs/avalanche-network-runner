package local

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

func TestWrongNetworkConfigs(t *testing.T) {
	networkConfigsJSON := []string{
		"",
		`{}`,
		`{"NodeConfigs":[]}`,
	}
	for _, networkConfigJSON := range networkConfigsJSON {
		err := networkStartWaitStop([]byte(networkConfigJSON))
		assert.Error(t, err)
	}
}

func TestBasicNetwork(t *testing.T) {
	networkConfigPath := "network_configs/basic_network.json"
	networkConfigJSON, err := readNetworkConfigJSON(networkConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := networkStartWaitStop(networkConfigJSON); err != nil {
		t.Fatal(err)
	}
}

func networkStartWaitStop(networkConfigJSON []byte) error {
	binMap, err := getBinMap()
	if err != nil {
		return err
	}
	networkConfig, err := getNetworkConfig(networkConfigJSON)
	if err != nil {
		return err
	}
	net, err := startNetwork(binMap, networkConfig)
	if err != nil {
		return err
	}
	if err := awaitNetwork(net); err != nil {
		return err
	}
	if err := stopNetwork(net); err != nil {
		return err
	}
	return nil
}

func getBinMap() (map[NodeType]string, error) {
	envVarName := "AVALANCHEGO_PATH"
	avalanchegoPath, ok := os.LookupEnv(envVarName)
	if !ok {
		return nil, fmt.Errorf("must define env var %s", envVarName)
	}
	envVarName = "BYZANTINE_PATH"
	byzantinePath, ok := os.LookupEnv(envVarName)
	if !ok {
		return nil, fmt.Errorf("must define env var %s", envVarName)
	}
	binMap := map[NodeType]string{
		AVALANCHEGO: avalanchegoPath,
		BYZANTINE:   byzantinePath,
	}
	return binMap, nil
}

func readNetworkConfigJSON(networkConfigPath string) ([]byte, error) {
	networkConfigJSON, err := ioutil.ReadFile(networkConfigPath)
	if err != nil {
		return nil, fmt.Errorf("couldn't read network config file %s: %s", networkConfigPath, err)
	}
	return networkConfigJSON, nil
}

func getNetworkConfig(networkConfigJSON []byte) (*network.Config, error) {
	var networkConfigMap map[string]interface{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfigMap); err != nil {
		return nil, fmt.Errorf("couldn't unmarshall network config json: %s", err)
	}
	networkConfig := network.Config{}
	if networkConfigMap["NodeConfigs"] != nil {
		for _, nodeConfigMap := range networkConfigMap["NodeConfigs"].([]interface{}) {
			nodeConfigMap := nodeConfigMap.(map[string]interface{})
			nodeConfig := node.Config{}
			if nodeConfigMap["Type"] != nil {
				nodeConfig.Type = NodeType(nodeConfigMap["Type"].(float64))
			}
			if nodeConfigMap["Name"] != nil {
				nodeConfig.Name = nodeConfigMap["Name"].(string)
			}
			if nodeConfigMap["StakingKey"] != nil {
				nodeConfig.StakingKey = []byte(nodeConfigMap["StakingKey"].(string))
			}
			if nodeConfigMap["StakingCert"] != nil {
				nodeConfig.StakingCert = []byte(nodeConfigMap["StakingCert"].(string))
			}
			if nodeConfigMap["ConfigFile"] != nil {
				nodeConfig.ConfigFile = []byte(nodeConfigMap["ConfigFile"].(string))
			}
			if nodeConfigMap["CChainConfigFile"] != nil {
				nodeConfig.CChainConfigFile = []byte(nodeConfigMap["CChainConfigFile"].(string))
			}
			if nodeConfigMap["GenesisFile"] != nil {
				nodeConfig.GenesisFile = []byte(nodeConfigMap["GenesisFile"].(string))
			}
			if nodeConfigMap["LogsToStdout"] != nil {
				nodeConfig.LogsToStdout = nodeConfigMap["LogsToStdout"].(bool)
			}
			networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
		}
	}
	return &networkConfig, nil
}

func startNetwork(binMap map[NodeType]string, networkConfig *network.Config) (network.Network, error) {
	var net network.Network
	net, err := NewNetwork(logging.NoLog{}, *networkConfig, binMap)
	if err != nil {
		return nil, err
	}
	return net, nil
}

func awaitNetwork(net network.Network) error {
	timeoutCh := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Minute)
		timeoutCh <- struct{}{}
	}()
	readyCh, errorCh := net.Ready()
	select {
	case <-readyCh:
		break
	case err := <-errorCh:
		return err
	case <-timeoutCh:
		return errors.New("network startup timeout")
	}
	return nil
}

func stopNetwork(net network.Network) error {
	return net.Stop()
}
