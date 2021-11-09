package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
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
	NetworkConfig network.Config `json:"networkConfig"`
	K8sConfig     []k8s.Config   `json:"k8sConfig"`
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
	config := readFiles(log, rconfig)
	config.ImplSpecificConfig = rconfig.K8sConfig

	level, err := logging.ToLevel(config.LogLevel)
	if err != nil {
		log.Warn("Invalid log level configured: %s", err)
	}
	log.SetLogLevel(level)
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

func readFiles(log logging.Logger, rconfig allConfig) network.Config {
	configDir := "./examples/common/configs"
	genesisFile, err := os.ReadFile(fmt.Sprintf("%s/genesis.json", configDir))
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
	netcfg := rconfig.NetworkConfig
	netcfg.Genesis = string(genesisFile)
	netcfg.NodeConfigs = make([]node.Config, 0)
	for i, k := range rconfig.K8sConfig {
		log.Info("reading config %d", i)
		nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
		key, err := os.ReadFile(fmt.Sprintf("%s/staking.key", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		cert, err := os.ReadFile(fmt.Sprintf("%s/staking.crt", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		c := node.Config{
			Name:               fmt.Sprintf("validator-%d", i),
			StakingCert:        cert,
			StakingKey:         key,
			ConfigFile:         configFile,
			ImplSpecificConfig: k,
		}
		if i == 0 {
			c.IsBeacon = true
		}
		netcfg.NodeConfigs = append(netcfg.NodeConfigs, c)
	}
	return netcfg
}
