package local

import (
	"fmt"
	"os/exec"

	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
)

// interface compliance
var _ node.Node = (*localNode)(nil)

// The type of this node (e.g. normal, byzantine, etc.)
// TODO Generalize this to allow user to specify a specific
// branch, etc. Or just have user provide path to binaries.
type NodeType int

const (
	AVALANCHEGO NodeType = iota + 1
	BYZANTINE
)

// Gives access to basic nodes info, and to most avalanchego apis
type localNode struct {
	// This ID is internal to the network runner.
	// It isn't an Avalanche Node ID.
	id ids.ID
	// [nodeID] is this node's Avalannche Node ID.
	nodeID ids.ShortID
	// Allows user to make API calls to this node.
	client api.Client
	// The command that started this node.
	// Send a SIGTERM to [cmd.Process] to stop this node.
	cmd *exec.Cmd
}

// Returs internal id used by the network runner
// Incremental from 1..
// 1:1 mapping to network config node positions
func (node *localNode) GetID() ids.ID {
	return node.id
}

// Returns this node's avalanchego node ID
func (node *localNode) GetNodeID() (ids.ShortID, error) {
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
func (node *localNode) GetAPIClient() api.Client {
	return node.client
}
