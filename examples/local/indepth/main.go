package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/e2e"
	"github.com/ava-labs/avalanche-network-runner/localbinary/runner"
	"github.com/ava-labs/avalanche-network-runner/networks"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/config"
)

// Run the local network and use a callback function to perform an additional modification to the network.
// 1) Add a new node
// 2) Shut down a node on the network
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		// register signals to kill the application
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT)
		signal.Notify(signals, syscall.SIGTERM)
		// Wait to receive one of these signals to terminate
		<-signals
		cancel()
	}()

	updatedNodesCallback := func(network backend.Network) error {
		nodes := network.GetNodes()
		if len(nodes) != 5 {
			return fmt.Errorf("network had unexpected number of nodes: %d", len(nodes))
		}

		// Add a new node and wait for the whole network to get healthy again
		nodeConfig := networks.CreateBasicLocalNodeConfig()
		// TODO need to specify the port here to ensure there is not a conflict. Need to switch to finding a free port
		// in the localbinary package if the ports are left as default values.
		nodeConfig[config.HTTPPortKey] = 9660
		nodeConfig[config.StakingPortKey] = 9661

		node := nodes[2]

		infoClient := info.NewClient(node.GetHTTPBaseURI())
		nodeID, err := infoClient.GetNodeID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get nodeID: %w", err)
		}

		nodeConfig[config.BootstrapIDsKey] = nodeID
		nodeConfig[config.BootstrapIPsKey] = node.GetBootstrapIP()

		_, err = network.AddNode(ctx, backend.NodeConfig{
			Name:       "updatedNode",
			Executable: constants.NormalExecution,
			Config:     nodeConfig,
		})
		if err != nil {
			return err
		}

		if err := e2e.AwaitHealthy(ctx, network, 5*time.Second); err != nil {
			return err
		}

		if err := node.Stop(10 * time.Second); err != nil {
			return err
		}

		return e2e.AwaitHealthy(ctx, network, 5*time.Second)
	}

	if err := runner.RunNetwork(ctx, os.Args[1:], updatedNodesCallback); err != nil {
		fmt.Printf("network exited due to: %s\n", err)
	}

	fmt.Printf("Shutdown network.\n")
}
