package main

import (
	"fmt"
	"os"
	"time"

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
	nodeConfig := node.Config{
		Type: local.AVALANCHEGO,
	}
	networkConfig := network.Config{
		NodeConfigs: []node.Config{
			nodeConfig,
		},
	}
	nw, err := local.NewNetwork(
		logging.NoLog{},
		networkConfig,
		map[local.NodeType]string{
			local.AVALANCHEGO: fmt.Sprintf("%s%s", goPath, "/src/github.com/ava-labs/avalanchego/build/avalanchego"),
		},
	)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	nodeNames := nw.GetNodesNames()
	time.Sleep(20 * time.Second) // Wait for node to set up
	node, err := nw.GetNode(nodeNames[0])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	nodeID, err := node.GetNodeID()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("this network has %d node and the first node's ID is %s\n", len(nodeNames), nodeID.PrefixedString(constants.NodeIDPrefix))
	if err := nw.Stop(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
