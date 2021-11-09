package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/units"
)

var goPath = os.ExpandEnv("$GOPATH")

// Start 6 nodes, wait for them to become healthy, then stop them all.
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

	// Create network config
	networkConfig := network.Config{
		NetworkID: 1337,
	}

	allNodeIDs := []ids.ShortID{}

	// Define the nodes to run when network is created.
	// Read config file from disk.
	configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner/examples/local/configs", goPath)
	configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", configDir))
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
	for i := 0; i < 6; i++ {
		var (
			stakingKey  []byte
			stakingCert []byte
		)
		// For first 3 nodes, read staking key/cert from disk
		if i < 3 {
			log.Info("reading node %d key/cert", i)
			nodeConfigDir := fmt.Sprintf("%s/node%d", configDir, i)
			stakingKey, err = os.ReadFile(fmt.Sprintf("%s/staking.key", nodeConfigDir))
			if err != nil {
				log.Fatal("%s", err)
				os.Exit(1)
			}
			stakingCert, err = os.ReadFile(fmt.Sprintf("%s/staking.crt", nodeConfigDir))
			if err != nil {
				log.Fatal("%s", err)
				os.Exit(1)
			}
		} else { // For the last 3 nodes, use random staking key/cert
			log.Info("generating node %d key/cert", i)
			stakingCert, stakingKey, err = staking.NewCertAndKeyBytes()
			if err != nil {
				log.Fatal("%s", err)
				os.Exit(1)
			}
		}
		// Node ID of the node we're adding to the initial network state
		nodeID, err := utils.ToNodeID(stakingKey, stakingCert)
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		networkConfig.NodeConfigs = append(
			networkConfig.NodeConfigs,
			node.Config{
				ImplSpecificConfig: local.NodeConfig{
					// Specify which binary to use to start AvalancheGo
					BinaryPath: binaryPath,
				},
				// Specify config file used by this node
				ConfigFile:  configFile,
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
				NodeID:      nodeID,
				// make this node a beacon
				IsBeacon: true,
			},
		)
		allNodeIDs = append(allNodeIDs, nodeID)
	}

	// Generate and set network genesis.
	// Give some random addresses an initial balance,
	// and make all nodes validators.
	// Note that you also read a genesis file from disk
	// and set [networkConfig.Genesis] to that.
	networkConfig.Genesis, err = network.NewAvalancheGoGenesis(
		log,
		networkConfig.NetworkID, // Network ID
		[]network.AddrAndBalance{ // X-Chain Balances
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 1,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 2,
			},
		},
		[]network.AddrAndBalance{ // C-Chain Balances
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 3,
			},
			{
				Addr:    ids.GenerateTestShortID(),
				Balance: units.KiloAvax + 4,
			},
		},
		allNodeIDs, // Make all nodes validators
	)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
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

	// When we get a SIGINT or SIGTERM, stop the network.
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	go func() {
		sig := <-signalsCh
		log.Info("got OS signal %s", sig)
		if err := nw.Stop(context.TODO()); err != nil {
			log.Warn("error while stopping network: %s", err)
		}
	}()

	// Wait until the nodes in the network are ready
	healthyChan := nw.Healthy()
	log.Info("waiting for all nodes to report healthy...")
	err, gotErr := <-healthyChan
	if gotErr {
		log.Fatal("network never became healthy: %s\n", err)
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
