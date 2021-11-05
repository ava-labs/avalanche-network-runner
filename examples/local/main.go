package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/local"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
)

var goPath = os.ExpandEnv("$GOPATH")

// Start a network with 1 node which connects to mainnet.
// Uses default configs.
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

	// Path to AvalancheGo binary
	binaryPath := fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")

	// Read node configs, staking keys, staking certs
	networkConfig := network.Config{}
	for i := 0; i < 5; i++ {
		log.Info("reading config %d", i)
		configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner-local/examples/local/configs", goPath)
		genesisFile, err := os.ReadFile(fmt.Sprintf("%s/genesis.json", configDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
		configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		stakingKey, err := os.ReadFile(fmt.Sprintf("%s/staking.key", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		stakingCert, err := os.ReadFile(fmt.Sprintf("%s/staking.crt", nodeConfigDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		networkConfig.NodeConfigs = append(
			networkConfig.NodeConfigs,
			node.Config{
				ImplSpecificConfig: local.NodeConfig{
					BinaryPath: binaryPath,
				},
				ConfigFile:  configFile,
				GenesisFile: genesisFile,
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
			},
		)
		if i == 0 {
			networkConfig.NodeConfigs[0].IsBeacon = true
		}
	}

	// Uncomment this line to print the first node's logs to stdout
	// networkConfig.NodeConfigs[0].Stdout = os.Stdout

	// Create the network
	nw, err := local.NewNetwork(
		log,
		networkConfig,
		api.NewAPIClient,
		local.NewNodeProcess,
	)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}

	// register signals to kill the network
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	// start up a new go routine to handle attempts to kill the application
	go func() {
		// When we get a SIGINT or SIGTERM, stop the network.
		sig := <-signalsCh
		log.Info("got OS signal %s", sig)
		if err := nw.Stop(context.TODO()); err != nil {
			log.Warn("error while stopping network: %s", err)
		}
	}()

	// Wait until the nodes in the network are ready
	healthyChan := nw.Healthy()
	fmt.Println("waiting for all nodes to report healthy...")
	err, gotErr := <-healthyChan
	if gotErr {
		log.Fatal("network never became healthy: %s", err)
		handleError(log, nw)
	}
	log.Info("this network's nodes: %s\n", nw.GetNodesNames())
	if err := nw.Stop(context.TODO()); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
}

func handleError(log logging.Logger, nw network.Network) {
	if err := nw.Stop(context.TODO()); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
	os.Exit(1)
}
