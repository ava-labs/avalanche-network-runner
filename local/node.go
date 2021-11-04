package local

import (
	"os/exec"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/ids"
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
	// Must be unique across all nodes in this network.
	name string
	// [nodeID] is this node's Avalannche Node ID.
	nodeID ids.ShortID
	// Allows user to make API calls to this node.
	client api.Client
	// The command that started this node.
	// Send a SIGTERM to [cmd.Process] to stop this node.
	cmd *exec.Cmd
}

// Return this node's unique name
func (node *localNode) GetName() string {
	return node.name
}

// Returns this node's avalanchego node ID
func (node *localNode) GetNodeID() ids.ShortID {
	return node.nodeID
}

// Returns access to avalanchego apis
func (node *localNode) GetAPIClient() api.Client {
	return node.client
}
