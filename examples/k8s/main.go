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
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	// defaultNetworkTimeout to wait network to come up until deemed failed
	defaultNetworkTimeout = 120 * time.Second
	configFileName        = "/conf.json"
)

// TODO: shouldn't we think of something like Viper for loading config file?
var goPath = os.ExpandEnv("$GOPATH")

// Network and node configs
type allConfig struct {
	NetworkConfig network.Config   `json:"networkConfig"`
	K8sConfig     []k8s.ObjectSpec `json:"k8sConfig"`
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	// Create the logger
	loggingConfig, err := logging.DefaultConfig()
	if err != nil {
		fmt.Println(err)
		return err
	}
	logFactory := logging.NewFactory(loggingConfig)
	log, err := logFactory.Make("main")
	if err != nil {
		fmt.Println(err)
		return err
	}

	configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner/examples/k8s", goPath)
	if goPath == "" {
		configDir = "./examples/k8s"
	}
	log.Info("reading config file...")
	configFile, err := os.ReadFile(configDir + configFileName)
	if err != nil {
		log.Fatal("%s", err)
		return err
	}

	// Network and node configs
	var allConfig allConfig
	if err := json.Unmarshal(configFile, &allConfig); err != nil {
		log.Fatal("%s", err)
		return err
	}

	// TODO maybe do networkConfig validation
	log.Info("parsing config...")
	networkConfig, err := readConfig(allConfig)
	if err != nil {
		log.Fatal("error reading configs: %s", err)
		return err
	}

	level, err := logging.ToLevel(networkConfig.LogLevel)
	if err != nil {
		log.Fatal("couldn't parse log: %s", err)
		return err
	}
	log.SetLogLevel(level)
	log.SetDisplayLevel(level)
	ctx, cancel := context.WithTimeout(context.Background(), defaultNetworkTimeout)
	defer cancel()

	network, err := k8s.NewNetwork(log, networkConfig)
	if err != nil {
		log.Fatal("Error creating network: %s", err)
		return err
	}
	defer func() {
		if err := network.Stop(ctx); err != nil {
			log.Error("Error stopping network (ignored): %s", err)
		}
	}()

	log.Info("Network created. Booting...")

	errCh := network.Healthy(ctx)

	select {
	case <-ctx.Done():
		log.Fatal("Timed out waiting for network to boot. Exiting.")
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			log.Fatal("Error booting network: %s", err)
			return err
		}
	}
	log.Info("Network created!!!")
	return nil
}

func readConfig(rconfig allConfig) (network.Config, error) {
	configDir := "./examples/k8s/configs"
	genesisFile, err := os.ReadFile(fmt.Sprintf("%s/genesis.json", configDir))
	if err != nil {
		return network.Config{}, err
	}
	netcfg := rconfig.NetworkConfig
	netcfg.Genesis = string(genesisFile)
	netcfg.NodeConfigs = make([]node.Config, 0)
	for i, k := range rconfig.K8sConfig {
		nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
		key, err := os.ReadFile(fmt.Sprintf("%s/staking.key", nodeConfigDir))
		if err != nil {
			return network.Config{}, err
		}
		cert, err := os.ReadFile(fmt.Sprintf("%s/staking.crt", nodeConfigDir))
		if err != nil {
			return network.Config{}, err
		}
		configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", nodeConfigDir))
		if err != nil {
			return network.Config{}, err
		}
		c := node.Config{
			Name:        fmt.Sprintf("validator-%d", i),
			StakingCert: string(cert),
			StakingKey:  string(key),
			ConfigFile:  string(configFile),
			ImplSpecificConfig: utils.NewK8sNodeConfigJsonRaw(
				k.APIVersion,
				k.Identifier,
				k.Image,
				k.Kind,
				k.Namespace,
				k.Tag,
			),
			IsBeacon: i == 0,
		}
		netcfg.NodeConfigs = append(netcfg.NodeConfigs, c)
	}
	return netcfg, nil
}
