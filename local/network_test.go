package local

import (
	_ "embed"
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
	networkConfigsJSON := []string{
		"",
		`{}`,
		`{"NodeConfigs":[]}`,
	}
	for _, networkConfigJSON := range networkConfigsJSON {
		networkConfig, err := ParseNetworkConfigJSON([]byte(networkConfigJSON))
		if err == nil {
			err := networkStartWaitStop(t, networkConfig)
			assert.Error(t, err)
		}
	}
}

func TestNetworkFromConfig(t *testing.T) {
	networkConfigPath := "network_config.json"
	networkConfigJSON, err := ioutil.ReadFile(networkConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := networkStartWaitStop(t, networkConfig); err != nil {
		t.Fatal(err)
	}
}

func networkStartWaitStop(t *testing.T, networkConfig *network.Config) error {
    var err error
	binMap, err := getBinMap()
	if err != nil {
		return err
	}
	net, err := startNetwork(binMap, networkConfig)
	if err != nil {
		return err
	}
	defer func() {
        err = net.Stop()
    }()
	if err := awaitNetwork(net); err != nil {
		return err
	}
	nodeIDs := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		node, err := net.GetNode(nodeConfig.Name)
		if err != nil {
			return err
		}
		client := node.GetAPIClient()
		nodeID, err := client.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		nodeIDs[nodeID] = true
	}
	assert.Equal(t, len(nodeIDs), len(networkConfig.NodeConfigs), "unique node ids count should be number of nodes in config")
	return err
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
