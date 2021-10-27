package localnetworkrunner

import (
	_ "embed"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/sirupsen/logrus"
)

//go:embed "network_config.json"
var networkConfigsJSON []byte

func TestNetworkStartStop(t *testing.T) {
	confs := map[string]interface{}{}
	if err := json.Unmarshal(networkConfigsJSON, &confs); err != nil {
		t.Fatalf("couldn't unmarshall network configs json: %s", err)
	}
	networkConfigJSON, err := json.Marshal(confs["2"])
	if err != nil {
		t.Fatalf("couldn't marshall network config json: %s", err)
	}
	binMap := getBinMap(t)
	networkConfig := getNetworkConfig(t, networkConfigJSON)
	net := startNetwork(t, binMap, networkConfig)
	awaitNetwork(t, net)
	stopNetwork(t, net)
}

func getBinMap(t *testing.T) map[int]string {
	envVarName := "AVALANCHEGO_PATH"
	avalanchegoPath, ok := os.LookupEnv(envVarName)
	if !ok {
		t.Fatalf("must define env var %s", envVarName)
	}
	envVarName = "BYZANTINE_PATH"
	byzantinePath, ok := os.LookupEnv(envVarName)
	if !ok {
		t.Fatalf("must define env var %s", envVarName)
	}
	binMap := map[int]string{
		networkrunner.AVALANCHEGO: avalanchegoPath,
		networkrunner.BYZANTINE:   byzantinePath,
	}
	return binMap
}

func getNetworkConfig(t *testing.T, networkConfigJSON []byte) networkrunner.NetworkConfig {
	networkConfig := networkrunner.NetworkConfig{}
	if err := json.Unmarshal(networkConfigJSON, &networkConfig); err != nil {
		t.Fatalf("couldn't unmarshall network config json: %s", err)
	}
	return networkConfig
}

func startNetwork(t *testing.T, binMap map[int]string, networkConfig networkrunner.NetworkConfig) networkrunner.Network {
	logger := logrus.New()
	var net networkrunner.Network
	net, err := NewNetwork(networkConfig, binMap, logger)
	if err != nil {
		t.Fatalf("couldn't create network: %s", err)
	}
	return net
}

func awaitNetwork(t *testing.T, net networkrunner.Network) {
	timeoutCh := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Minute)
		timeoutCh <- struct{}{}
	}()
	readyCh, errorCh := net.Ready()
	t.Log("waiting for network startup")
	select {
	case <-readyCh:
		break
	case err := <-errorCh:
		t.Fatal(err)
	case <-timeoutCh:
		t.Fatal("network startup timeout")
	}
}

func stopNetwork(t *testing.T, net networkrunner.Network) {
	err := net.Stop()
	if err != nil {
		t.Fatalf("couldn't cleanly stop network: %s", err)
	}
}
