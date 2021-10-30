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
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

func TestWrongNetworkConfigs(t *testing.T) {
	networkConfigsJSON := []string{
		"",
	}
	for _, networkConfigJSON := range networkConfigsJSON {
		networkConfig, err := ParseNetworkConfigJSON([]byte(networkConfigJSON))
		if err == nil {
			_, err := networkStartWait(t, networkConfig)
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
	net, err := networkStartWait(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = net.Stop()
	}()
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = true
	}
	if err := checkNetwork(t, net, runningNodes, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkNodeOps(t *testing.T) {
	networkConfigPath := "network_config.json"
	networkConfigJSON, err := ioutil.ReadFile(networkConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	net, err := networkStartWait(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = net.Stop()
	}()
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err = net.AddNode(nodeConfig)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		runningNodes[nodeConfig.Name] = true
		if err := checkNetwork(t, net, runningNodes, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitNetwork(net); err != nil {
		t.Fatal(err)
	}
	var removedClients []api.Client
	for _, nodeConfig := range networkConfig.NodeConfigs {
		node, err := net.GetNode(nodeConfig.Name)
		if err != nil {
			t.Fatal(err)
		}
		client := node.GetAPIClient()
		removedClients = append(removedClients, client)
		err = net.RemoveNode(nodeConfig.Name)
		if err != nil {
			t.Fatal(err)
		}
		delete(runningNodes, nodeConfig.Name)
		if err := checkNetwork(t, net, runningNodes, removedClients); err != nil {
			t.Fatal(err)
		}
	}
}

func networkStartWait(t *testing.T, networkConfig *network.Config) (network.Network, error) {
	binMap, err := getBinMap()
	if err != nil {
		return nil, err
	}
	net, err := NewNetwork(logging.NoLog{}, *networkConfig, binMap)
	if err != nil {
		return nil, err
	}
	if err := awaitNetwork(net); err != nil {
		_ = net.Stop()
		return nil, err
	}
	return net, nil
}

func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]bool, removedClients []api.Client) error {
	nodeNames := net.GetNodesNames()
	if len(nodeNames) != len(runningNodes) {
		return fmt.Errorf("GetNodesNames() len %v should equal number of running nodes %v", len(nodeNames), len(runningNodes))
	}
	for nodeName := range runningNodes {
		node, err := net.GetNode(nodeName)
		if err != nil {
			return err
		}
		client := node.GetAPIClient()
		if _, err := client.InfoAPI().GetNodeID(); err != nil {
			return err
		}
	}
	for _, client := range removedClients {
		nodeID, err := client.InfoAPI().GetNodeID()
		if err == nil {
			return fmt.Errorf("removed node %v is answering requests", nodeID)
		}
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

func awaitNetwork(net network.Network) error {
	timeoutCh := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Minute)
		timeoutCh <- struct{}{}
	}()
	readyCh := net.Ready()
	select {
	case <-readyCh:
		break
	case <-timeoutCh:
		return errors.New("network startup timeout")
	}
	return nil
}

// TODO do we need this? It isn't used anywhere.
func ParseNetworkConfigJSON(networkConfigJSON []byte) (*network.Config, error) {
	var networkConfigMap map[string]interface{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfigMap); err != nil {
		return nil, fmt.Errorf("couldn't unmarshall network config json: %s", err)
	}
	networkConfig := network.Config{}
	var networkGenesisFile []byte
	var networkCChainConfigFile []byte
	if networkConfigMap["GenesisFile"] != nil {
		networkGenesisFile = []byte(networkConfigMap["GenesisFile"].(string))
	}
	if networkConfigMap["CChainConfigFile"] != nil {
		networkCChainConfigFile = []byte(networkConfigMap["CChainConfigFile"].(string))
	}
	if networkConfigMap["NodeConfigs"] != nil {
		for _, nodeConfigMap := range networkConfigMap["NodeConfigs"].([]interface{}) {
			nodeConfigMap := nodeConfigMap.(map[string]interface{})
			nodeConfig := node.Config{}
			nodeConfig.GenesisFile = networkGenesisFile
			nodeConfig.CChainConfigFile = networkCChainConfigFile
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
			networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
		}
	}
	return &networkConfig, nil
}
