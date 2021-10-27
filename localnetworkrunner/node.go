package localnetworkrunner

import (
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
)

// Gives access to basic nodes info, and to most avalanchego apis
type Node struct {
	// This ID is internal to the network runner.
	// It isn't an Avalanche Node ID.
	id ids.ID
	// [nodeID] is this node's Avalannche Node ID.
	nodeID ids.ShortID
	// Allows user to make API calls to this node.
	client APIClient
}

// interface compliance
var _ networkrunner.Node = (*Node)(nil)

// Returs internal id used by the network runner
// Incremental from 1..
// 1:1 mapping to network config node positions
func (node *Node) GetID() ids.ID {
	return node.id
}

// Returns this node's avalanchego node ID
func (node *Node) GetNodeID() (ids.ShortID, error) {
	if node.nodeID != ids.ShortEmpty {
		return node.nodeID, nil
	}
	info := node.client.InfoAPI()
	strNodeID, err := info.GetNodeID()
	if err != nil {
		return ids.ShortID{}, fmt.Errorf("could not obtain node ID from info api: %s", err)
	}
	nodeID, err := ids.ShortFromPrefixedString(strNodeID, constants.NodeIDPrefix)
	if err != nil {
		return ids.ShortID{}, fmt.Errorf("could not parse node ID from string: %s", err)
	}
	node.nodeID = nodeID
	return node.nodeID, nil
}

// Returns access to avalanchego apis
func (node *Node) GetAPIClient() networkrunner.APIClient {
	return node.client
}
