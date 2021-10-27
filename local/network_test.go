package localnetworkrunner

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
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

func TestWrongNetworkConfigs(t *testing.T) {
	tests := []struct {
		networkConfigJSON string
	}{
		{
			networkConfigJSON: "network_configs/empty_config.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_1.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_2.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_3.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_4.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_5.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_network_config_6.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_01.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_02.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_03.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_04.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_05.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_06.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_07.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_08.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_09.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_10.json",
		},
	}
	for _, tt := range tests {
		networkConfigJSON, err := readNetworkConfigJSON(tt.networkConfigJSON)
		if err != nil {
			t.Fatal(err)
		}
		err = networkStartWaitStop(networkConfigJSON)
		assert.Error(t, err)
	}
}

func TestBasicNetwork(t *testing.T) {
	t.Skip()
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

func getBinMap() (map[nodeType]string, error) {
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
	binMap := map[nodeType]string{
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
	networkConfig := network.Config{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfig); err != nil {
		return nil, fmt.Errorf("couldn't unmarshall network config json: %s", err)
	}
	return &networkConfig, nil
}

func startNetwork(binMap map[nodeType]string, networkConfig *network.Config) (network.Network, error) {
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
