package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	numNodes       = 5
	healthyTimeout = 2 * time.Minute
)

var (
	//go:embed configs
	embeddedConfigsDir embed.FS
	goPath             = os.ExpandEnv("$GOPATH")
	binaryPath         string // TODO read flag
)

func init() {
	binaryPath = fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
}

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
	if err := run(log); err != nil {
		log.Fatal("%s", err)
	}
}

func run(log logging.Logger) error {
	// Read configs to create network config
	networkConfig, err := readConfigs()
	if err != nil {
		return fmt.Errorf("couldn't read configs: %w", err)
	}

	// Create the network
	nw, err := local.NewNetwork(
		log,
		networkConfig,
		api.NewAPIClient,
		local.NewNodeProcess,
	)
	if err != nil {
		return err
	}

	// When we get a SIGINT or SIGTERM, stop the network.
	signalsCh := make(chan os.Signal, 1)
	signal.Notify(signalsCh, syscall.SIGINT)
	signal.Notify(signalsCh, syscall.SIGTERM)
	go func() {
		sig := <-signalsCh
		log.Info("got OS signal %s", sig)
		if err := nw.Stop(context.Background()); err != nil {
			log.Warn("error while stopping network: %s", err)
		}
	}()

	// Wait until the nodes in the network are ready
	timeout, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	healthyChan := nw.Healthy(timeout)
	log.Info("waiting for all nodes to report healthy...")
	err, gotErr := <-healthyChan
	if gotErr {
		return err
	}

	// Print the node names
	nodeNames, err := nw.GetNodesNames()
	if err != nil {
		return err
	}
	log.Info("this network's nodes: %s", nodeNames)

	// Get one node
	node, err := nw.GetNode(nodeNames[0])
	if err != nil {
		return err
	}

	// Get its node ID through its API and print it
	nodeID, err := node.GetAPIClient().InfoAPI().GetNodeID()
	if err != nil {
		return err
	}
	log.Info("one node's ID is: %s", nodeID)
	log.Info("example program done")
	if err := nw.Stop(context.Background()); err != nil {
		log.Warn("error while stopping network: %s", err)
	}
	return nil
}

func readConfigs() (network.Config, error) {
	configsDir, err := fs.Sub(embeddedConfigsDir, "configs")
	if err != nil {
		return network.Config{}, err
	}

	config := network.Config{
		Name:        "my network",
		NodeConfigs: make([]node.Config, numNodes),
		LogLevel:    "INFO",
	}

	config.Genesis, err = fs.ReadFile(configsDir, "genesis.json")
	if err != nil {
		return network.Config{}, err
	}

	for i := 0; i < len(config.NodeConfigs); i++ {
		config.NodeConfigs[i].ConfigFile, err = fs.ReadFile(configsDir, fmt.Sprintf("node%d/config.json", i))
		if err != nil {
			return network.Config{}, fmt.Errorf("couldn't read node %d config.json: %w", i, err)
		}
		config.NodeConfigs[i].StakingKey, err = fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.key", i))
		if err != nil {
			return network.Config{}, fmt.Errorf("couldn't read node %d staker.key: %w", i, err)
		}
		config.NodeConfigs[i].StakingCert, err = fs.ReadFile(configsDir, fmt.Sprintf("node%d/staking.crt", i))
		if err != nil {
			return network.Config{}, fmt.Errorf("couldn't read node %d staker.crt: %w", i, err)
		}
		config.NodeConfigs[i].ImplSpecificConfig = local.NodeConfig{
			BinaryPath: binaryPath,
		}
		config.NodeConfigs[i].IsBeacon = true
	}
	return config, nil
}
