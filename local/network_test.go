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
		networkConfigPath string
		expectedError     error
	}{
		{
			networkConfigPath: "network_configs/empty_config.json",
			expectedError:     errors.New("couldn't unmarshall network config json: unexpected end of JSON input"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_1.json",
			expectedError:     errors.New("incomplete network config: Genesis field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_2.json",
			expectedError:     errors.New("incomplete network config: CChainConfig field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_3.json",
			expectedError:     errors.New("incomplete network config: CoreConfigFlags field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_4.json",
			expectedError:     errors.New("couldn't unmarshal core config flags: unexpected end of JSON input"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_5.json",
			expectedError:     errors.New("incomplete network config: NodeConfigs field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_6.json",
			expectedError:     errors.New("incomplete network config: NodeConfigs field must have at least a node"),
		},
	}
	for _, tt := range tests {
		givenErr := networkStartWaitStop(tt.networkConfigPath)
		assert.Equal(t, tt.expectedError, givenErr, fmt.Sprintf("path: %s", tt.networkConfigPath))
	}
}

func TestBasicNetwork(t *testing.T) {
	t.Skip()
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

func getBinMap() (map[int]string, error) {
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
	binMap := map[int]string{
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

func startNetwork(binMap map[int]string, networkConfig *network.Config) (network.Network, error) {
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
