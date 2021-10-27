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

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/sirupsen/logrus"
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
			expectedError:     errors.New("config failed validation: genesis field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_2.json",
			expectedError:     errors.New("config failed validation: cChainConfig field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_3.json",
			expectedError:     errors.New("config failed validation: coreConfigFlags field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_4.json",
			expectedError:     errors.New("couldn't unmarshal core config flags: unexpected end of JSON input"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_5.json",
			expectedError:     errors.New("config failed validation: nodeConfigs field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_network_config_6.json",
			expectedError:     errors.New("config failed validation: nodeConfigs field must have at least a node"),
		},
		{
			networkConfigPath: "network_configs/incomplete_node_config_1.json",
			expectedError:     errors.New("incomplete node config for node 1: BinKind field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_node_config_2.json",
			expectedError:     errors.New("incomplete node config for node 1: ConfigFlags field is empty"),
		},
		{
			networkConfigPath: "network_configs/incomplete_node_config_3.json",
			expectedError:     errors.New("couldn't unmarshal config flags for node 1: unexpected end of JSON input"),
		},
		{
			networkConfigPath: "network_configs/incomplete_node_config_4.json",
			expectedError:     errors.New("node 1 lacks config flag chain-config-dir"),
		},
	}
	for _, tt := range tests {
		givenErr := networkStartWaitStop(tt.networkConfigPath)
		assert.Equal(t, tt.expectedError.Error(), givenErr.Error(), fmt.Sprintf("path: %s", tt.networkConfigPath))
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
		networkrunner.AVALANCHEGO: avalanchegoPath,
		networkrunner.BYZANTINE:   byzantinePath,
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

func getNetworkConfig(networkConfigJSON []byte) (*networkrunner.NetworkConfig, error) {
	networkConfig := networkrunner.NetworkConfig{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfig); err != nil {
		return nil, fmt.Errorf("couldn't unmarshall network config json: %s", err)
	}
	return &networkConfig, nil
}

func startNetwork(binMap map[uint]string, networkConfig *networkrunner.NetworkConfig) (networkrunner.Network, error) {
	logger := logrus.New()
	var net networkrunner.Network
	net, err := NewNetwork(*networkConfig, binMap, logger)
	if err != nil {
		return nil, err
	}
	return net, nil
}

func awaitNetwork(net networkrunner.Network) error {
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

func stopNetwork(net networkrunner.Network) error {
	err := net.Stop()
	if err != nil {
		return fmt.Errorf("couldn't cleanly stop network: %s", err)
	}
	return nil
}
