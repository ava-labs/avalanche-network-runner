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
	"github.com/ava-labs/avalanchego/staking"
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
)

// Blocks until a signal is received on [signalChan], upon which
// [n.Stop()] is called. If [signalChan] is closed, does nothing.
// Closes [closedOnShutdownChan] amd [signalChan] when done shutting down network.
// This function should only be called once.
func shutdownOnSignal(
	log logging.Logger,
	n network.Network,
	signalChan chan os.Signal,
	closedOnShutdownChan chan struct{},
) {
	sig := <-signalChan
	log.Info("got OS signal %s", sig)
	if err := n.Stop(context.Background()); err != nil {
		log.Debug("error while stopping network: %s", err)
	}
	signal.Reset()
	close(signalChan)
	close(closedOnShutdownChan)
}

// Example:
// - start some nodes
// - wait for them to become healthy
// - get a node and make an API call to it
// - add a new node
// - remove a node
// - stop the network
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
	binaryPath := fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego")
	if err := run(log, binaryPath); err != nil {
		log.Fatal("%s", err)
	}
}

func run(log logging.Logger, binaryPath string) error {
	// Read configs to create network config
	networkConfig, err := readConfigs(binaryPath)
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
	defer func() { // Stop the network when this function returns
		if err := nw.Stop(context.Background()); err != nil {
			log.Debug("error stopping network: %w", err)
		}
	}()

	// When we get a SIGINT or SIGTERM, stop the network and close [closedOnShutdownCh]
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT)
	signal.Notify(signalsChan, syscall.SIGTERM)
	closedOnShutdownCh := make(chan struct{})
	go func() {
		shutdownOnSignal(log, nw, signalsChan, closedOnShutdownCh)
	}()

	// Wait until the nodes in the network are ready
	ctx, cancel := context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	healthyChan := nw.Healthy(ctx)
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
	log.Info("current network's nodes: %s", nodeNames)

	// Get one node
	node0, err := nw.GetNode(nodeNames[0])
	if err != nil {
		return err
	}

	// Get its node ID through its API and print it
	node0ID, err := node0.GetAPIClient().InfoAPI().GetNodeID()
	if err != nil {
		return err
	}
	log.Info("one node's ID is: %s", node0ID)

	// Add a new node with generated cert/key/nodeid
	stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
	if err != nil {
		return err
	}
	nodeConfig := node.Config{
		Name: "New Node",
		ImplSpecificConfig: local.NodeConfig{
			BinaryPath: binaryPath,
		},
		StakingKey:  stakingKey,
		StakingCert: stakingCert,
	}
	if _, err := nw.AddNode(nodeConfig); err != nil {
		return err
	}

	// Remove one node
	nodeToRemove := nodeNames[3]
	log.Info("removing node %q", nodeToRemove)
	if err := nw.RemoveNode(nodeToRemove); err != nil {
		return err
	}

	// Wait until the nodes in the updated network are ready
	ctx, cancel = context.WithTimeout(context.Background(), healthyTimeout)
	defer cancel()
	healthyChan = nw.Healthy(ctx)
	log.Info("waiting for updated network to report healthy...")
	err, gotErr = <-healthyChan
	if gotErr {
		return err
	}

	// Print the node names
	nodeNames, err = nw.GetNodesNames()
	if err != nil {
		return err
	}
	// Will have the new node but not the removed one
	log.Info("updated network's nodes: %s", nodeNames)
	log.Info("Network will run until you CTRL + C to exit...")
	// Wait until done shutting down network after SIGINT/SIGTERM
	<-closedOnShutdownCh
	return nil
}

func readConfigs(binaryPath string) (network.Config, error) {
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
