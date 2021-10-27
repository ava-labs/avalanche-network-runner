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
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
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
			networkConfigJSON: "network_configs/incomplete_node_config_1.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_2.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_3.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_4.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_5.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_6.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_7.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_8.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_9.json",
		},
		{
			networkConfigJSON: "network_configs/incomplete_node_config_10.json",
		},
	}
	for _, tt := range tests {
		err := networkStartWaitStop(tt.networkConfigJSON)
		assert.Error(t, err)
	}
}

func TestBasicNetwork(t *testing.T) {
	networkConfigPath := "network_configs/basic_network.json"
	if err := networkStartWaitStop(networkConfigPath); err != nil {
		t.Fatal(err)
	}
}

func networkStartWaitStop(networkConfigPath string) error {
	binMap, err := getBinMap()
	if err != nil {
		return err
	}
	networkConfigJSON, err := readNetworkConfigJSON(networkConfigPath)
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

func getBinMap() (map[uint]string, error) {
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
	binMap := map[uint]string{
		node.AVALANCHEGO: avalanchegoPath,
		node.BYZANTINE:   byzantinePath,
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

func startNetwork(binMap map[uint]string, networkConfig *network.Config) (network.Network, error) {
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
