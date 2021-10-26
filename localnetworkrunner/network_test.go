package localnetworkrunner

import (
	_ "embed"
    "testing"
    "os"
    "encoding/json"
    "time"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
    "github.com/sirupsen/logrus"
)

//go:embed "network_config.json"
var networkConfigJSON []byte

func TestNetworkStartStop(t *testing.T) {
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
    networkConfig := networkrunner.NetworkConfig{}
    if err := json.Unmarshal(networkConfigJSON, &networkConfig); err != nil {
        t.Fatalf("couldn't unmarshall network config json: %s", err)
    }
    logger := logrus.New()
    var net networkrunner.Network
    net, err := NewNetwork(networkConfig, binMap, logger)
    if err != nil {
        t.Fatalf("couldn't create network: %s", err)
    }
    timeoutCh := make(chan struct{})
    go func() {
        time.Sleep(5*time.Minute)
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
    err = net.Stop()
    if err != nil {
        t.Fatalf("couldn't cleanly stop network: %s", err)
    }
}

