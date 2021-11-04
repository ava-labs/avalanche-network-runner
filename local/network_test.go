package local

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/stretchr/testify/assert"
)

//go:embed network_config.json
var networkConfigJSON []byte

func TestWrongNetworkConfigs(t *testing.T) {
	networkConfigsJSON := []string{
		// no json
		"",
		// no type
		`{"NodeConfigs": [{"IsBeacon": true, "ConfigFile": "notempty", "GenesisFile": "notempty"}]}`,
		// no config
		`{"NodeConfigs": [{"IsBeacon": true, "Type": 1, "GenesisFile": "notempty"}]}`,
		// no genesis
		`{"NodeConfigs": [{"IsBeacon": true, "Type": 1, "ConfigFile": "notempty"}]}`,
		// staking key but no staking cert
		`{"NodeConfigs": [{"IsBeacon": true, "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty", "StakingKey": "notempty"}]}`,
		// staking cert but no staking key
		`{"NodeConfigs": [{"IsBeacon": true, "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty", "StakingCert": "notempty"}]}`,
		// different genesis file
		`{"NodeConfigs": [{"IsBeacon": true, "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty1"},{"Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty2"}]}`,
		// first node is not beacon
		`{"NodeConfigs": [{"Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty1"}]}`,
		// repeated name
		`{"NodeConfigs": [{"IsBeacon": true, "Name": "node1", "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty1"},{"Name": "node1", "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty1"}]}`,
		// type not found
		`{"NodeConfigs": [{"IsBeacon": true, "Name": "node1", "Type": 3, "ConfigFile": "notempty", "GenesisFile": "notempty1"}]}`,
		// invalid cert/key format
		`{"NodeConfigs": [{"IsBeacon": true, "Name": "node1", "Type": 1, "ConfigFile": "notempty", "GenesisFile": "notempty1", "StakingCert": "notempty", "StakingKey": "notempty"}]}`,
	}
	for _, networkConfigJSON := range networkConfigsJSON {
		networkConfig, err := ParseNetworkConfigJSON([]byte(networkConfigJSON))
		if err == nil {
			_, err := startNetwork(t, networkConfig)
			assert.Error(t, err)
		}
	}
}

func TestNodeTypeInterface(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	networkConfig.NodeConfigs[0].Type = 1
	_, err = startNetwork(t, networkConfig)
	if err == nil {
		t.Fatal(err)
	}
}

func TestInvalidCommand(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	binMap := map[NodeType]string{
		AVALANCHEGO: "unknown",
		BYZANTINE:   "unknown",
	}
	_, err = NewNetwork(logging.NoLog{}, *networkConfig, binMap)
	if err == nil {
		t.Fatal(err)
	}
}

func TestUnhealthyNetwork(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	networkConfig.NodeConfigs[0].ConfigFile = []byte(`{"network-id":1}`)
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	if awaitNetwork(net) == nil {
		t.Fatal(errors.New("await for unhealthy network was successful"))
	}
}

func TestGeneratedNodesNames(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	for i := range networkConfig.NodeConfigs {
		networkConfig.NodeConfigs[i].Name = ""
	}
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	nodeNameMap := make(map[string]bool)
	nodeNames, err := net.GetNodesNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, nodeName := range nodeNames {
		nodeNameMap[nodeName] = true
	}
	if len(nodeNameMap) != len(networkConfig.NodeConfigs) {
		t.Fatal(fmt.Errorf("number of unique node names in network %v differs from number of nodes in config %v", len(nodeNameMap), len(networkConfig.NodeConfigs)))
	}
}

// TODO add byzantine node to conf
// TestNetworkFromConfig creates/waits/checks/stops a network from config file
// the check verify that all the nodes api clients are up
func TestNetworkFromConfig(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, networkConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	if err := awaitNetwork(net); err != nil {
		t.Fatal(err)
	}
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		runningNodes[nodeConfig.Name] = true
	}
	if err := checkNetwork(t, net, runningNodes, nil); err != nil {
		t.Fatal(err)
	}
}

// TestNetworkNodeOps creates/waits/checks/stops a network created from an empty one
// nodes are first added one by one, then removed one by one. between all operations, a network check is performed
// the check verify that all the nodes api clients are up for started nodes, and down for removed nodes
// all nodes are taken from config file
func TestNetworkNodeOps(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	runningNodes := make(map[string]bool)
	for _, nodeConfig := range networkConfig.NodeConfigs {
		_, err = net.AddNode(nodeConfig)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(1 * time.Second)
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

// TestStoppedNetwork checks operations fail for unkown node
func TestNodeNotFound(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	if err != nil {
		t.Fatal(err)
	}
	// get correct node
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	// get uncorrect node (non created)
	_, err = net.GetNode(networkConfig.NodeConfigs[1].Name)
	if err == nil {
		t.Fatal(err)
	}
	// remove uncorrect node (non created)
	err = net.RemoveNode(networkConfig.NodeConfigs[1].Name)
	if err == nil {
		t.Fatal(err)
	}
	// give some time to the node to start, otherwise Wait() gets "signal: terminated" error
	time.Sleep(1 * time.Second)
	// remove correct node
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	// get uncorrect node (removed)
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err == nil {
		t.Fatal(err)
	}
	// remove uncorrect node (removed)
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err == nil {
		t.Fatal(err)
	}
	ctx := context.TODO()
	err = net.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

// TestStoppedNetwork checks operations fail for an already stopped network
func TestStoppedNetwork(t *testing.T) {
	networkConfig, err := ParseNetworkConfigJSON(networkConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	net, err := startNetwork(t, &network.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = net.AddNode(networkConfig.NodeConfigs[0])
	if err != nil {
		t.Fatal(err)
	}
	// first GetNodesNames should return some nodes
	_, err = net.GetNodesNames()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.TODO()
	err = net.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Stop does not fail
	err = net.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// AddNode failure
	_, err = net.AddNode(networkConfig.NodeConfigs[1])
	if err != errStopped {
		t.Fatal(err)
	}
	// GetNode failure
	_, err = net.GetNode(networkConfig.NodeConfigs[0].Name)
	if err != errStopped {
		t.Fatal(err)
	}
	// second GetNodesNames should return no nodes
	_, err = net.GetNodesNames()
	if err != errStopped {
		t.Fatal(err)
	}
	// RemoveNode failure
	err = net.RemoveNode(networkConfig.NodeConfigs[0].Name)
	if err != errStopped {
		t.Fatal(err)
	}
	// Healthy failure
	err = awaitNetwork(net)
	if err != errStopped {
		t.Fatal(err)
	}
}

func startNetwork(t *testing.T, networkConfig *network.Config) (network.Network, error) {
	binMap, err := getBinMap()
	if err != nil {
		return nil, err
	}
	net, err := NewNetwork(logging.NoLog{}, *networkConfig, binMap)
	if err != nil {
		return nil, err
	}
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	go func() {
		<-signalsCh
		ctx := context.TODO()
		_ = net.Stop(ctx)
	}()
	return net, nil
}

func checkNetwork(t *testing.T, net network.Network, runningNodes map[string]bool, removedClients []api.Client) error {
	nodeNames, err := net.GetNodesNames()
	if err != nil {
		return err
	}
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
	healthyCh := net.Healthy()
	select {
	case err, ok := <-healthyCh:
		if ok {
			ctx := context.TODO()
			_ = net.Stop(ctx)
			return err
		}
	case <-timeoutCh:
		ctx := context.TODO()
		_ = net.Stop(ctx)
		return errors.New("network startup timeout")
	}
	return nil
}

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
			if nodeConfigMap["IsBeacon"] != nil {
				nodeConfig.IsBeacon = nodeConfigMap["IsBeacon"].(bool)
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
			if nodeConfigMap["Stdout"] != nil {
				nodeConfig.Stdout = os.Stdout
			}
			if nodeConfigMap["Stderr"] != nil {
				nodeConfig.Stderr = os.Stderr
			}
			networkConfig.NodeConfigs = append(networkConfig.NodeConfigs, nodeConfig)
		}
	}
	return &networkConfig, nil
}
