package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	// defaultNetworkTimeout to wait network to come up until deemed failed
	defaultNetworkTimeout = 120 * time.Second
	confFileName          = "/conf.json"
)

// TODO: shouldn't we think of something like Viper for loading config file?
var confPath = os.ExpandEnv("$GOPATH")

type allConfig struct {
	NetworkConfig network.Config `json:"network_config"`
	K8sConfig     k8s.Config     `json:"k8s_config"`
}

func main() {
	// Create the logger
	loggingConfig, err := logging.DefaultConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	logFactory := logging.NewFactory(loggingConfig)
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner/examples/k8s", confPath)
	if confPath == "" {
		configDir = "./examples/k8s"
	}
	confFile, err := os.ReadFile(configDir + confFileName)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	var rconfig allConfig
	err = json.Unmarshal(confFile, &rconfig)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	// TODO maybe do config validation
	config := rconfig.NetworkConfig
	rconfig.K8sConfig.Certificates,
		rconfig.K8sConfig.CertKeys,
		rconfig.K8sConfig.Genesis = readFiles(log, config.NodeCount)
	config.ImplSpecificConfig = rconfig.K8sConfig

	ctx, cancel := context.WithTimeout(context.Background(), defaultNetworkTimeout)
	defer cancel()

	adapter, err := k8s.NewNetwork(config, log)
	if err != nil {
		log.Fatal("Error creating network: %s", err)
		os.Exit(1)
	}
	defer func() {
		if err := adapter.Stop(ctx); err != nil {
			log.Error("Error stopping network (ignored): %s", err)
		}
	}()

	log.Info("Network created. Booting...")

	errCh := adapter.Healthy()

	select {
	case <-ctx.Done():
		log.Fatal("Timed out waiting for network to boot. Exiting.")
		os.Exit(1)
	case err := <-errCh:
		if err != nil {
			log.Fatal("Error booting network: %s", err)
			os.Exit(1)
		}
	}
	log.Info("Network created!!!")
}

func readFiles(log logging.Logger, nodeCount int) ([][]byte, [][]byte, string) {
	certs := make([][]byte, nodeCount)
	keys := make([][]byte, nodeCount)
	configDir := "./examples/common/configs"
	genesisFile, err := os.ReadFile(fmt.Sprintf("%s/genesis.json", configDir))
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
	for i := 0; i < nodeCount; i++ {
		log.Info("reading config %d", i)
		nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
		keys[i], err = os.ReadFile(fmt.Sprintf("%s/staking.key", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		certs[i], err = os.ReadFile(fmt.Sprintf("%s/staking.crt", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
	}
	return certs, keys, string(genesisFile)
}
