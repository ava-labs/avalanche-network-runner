package main

import (
	"fmt"
	"os"

	"github.com/ava-labs/avalanche-network-runner-local/local"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/utils/constants"
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

	networkConfig := network.Config{}
	for i := 0; i < 5; i++ {
		configDir := fmt.Sprintf("%s/src/github.com/ava-labs/avalanche-network-runner-local/example/configs/node%d", goPath, i)
		configFile, err := os.ReadFile(fmt.Sprintf("%s/config.json", configDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		stakingKey, err := os.ReadFile(fmt.Sprintf("%s/staking.key", configDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		stakingCert, err := os.ReadFile(fmt.Sprintf("%s/staking.crt", configDir))
		if err != nil {
			log.Fatal("%s", err)
			os.Exit(1)
		}
		networkConfig.NodeConfigs = append(
			networkConfig.NodeConfigs,
			node.Config{
				Type:        local.AVALANCHEGO,
				APIPort:     9650 + 2*uint(i),
				ConfigFile:  configFile,
				StakingKey:  stakingKey,
				StakingCert: stakingCert,
			},
		)
	}

	// Create the network
	nw, err := local.NewNetwork(
		log,
		networkConfig,
		map[local.NodeType]string{
			local.AVALANCHEGO: fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego"),
		},
	)
	if err != nil {
		log.Fatal("%s", err)
		os.Exit(1)
	}
	// Wait until the nodes in the network are ready
	readyCh := nw.Ready()
	fmt.Println("waiting for all nodes to report healthy...")
	err, ok := <-readyCh
	if ok {
		log.Fatal("network never became healthy: %s\n", err)
		handleError(log, nw)
	}
	nodeNames := nw.GetNodesNames()
	node, err := nw.GetNode(nodeNames[0])
	if err != nil {
		log.Fatal("%s", err)
		handleError(log, nw)
	}
	nodeID, err := node.GetNodeID()
	if err != nil {
		log.Fatal("%s", err)
		handleError(log, nw)
	}
	log.Info("this network has %d nodes and the first node's ID is %s\n", len(nodeNames), nodeID.PrefixedString(constants.NodeIDPrefix))
	if err := nw.Stop(); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
}

func handleError(log logging.Logger, nw network.Network) {
	if err := nw.Stop(); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
	os.Exit(1)
}
