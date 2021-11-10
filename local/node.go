package local

import (
	"io"
	"os/exec"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
)

// interface compliance
var (
	_ node.Node   = (*localNode)(nil)
	_ NodeProcess = (*nodeProcessImpl)(nil)
)

// Node configurations which are specific to the
// local implementation of a network / node.
type NodeConfig struct {
	// What type of node this is
	BinaryPath string
	// If non-nil, direct this node's stdout here
	Stdout io.Writer
	// If non-nil, direct this node's stderr here
	Stderr io.Writer
}

// Use an interface so we can mock running
// AvalancheGo binaries in tests
type NodeProcess interface {
	// Start this process
	Start() error
	// Send a SIGTERM to this process
	Stop() error
	// Returns when the process finishes exiting
	Wait() error
}

type nodeProcessImpl struct {
	cmd *exec.Cmd
}

func (p *nodeProcessImpl) Start() error {
	return p.cmd.Start()
}

func (p *nodeProcessImpl) Wait() error {
	return p.cmd.Wait()
}

func (p *nodeProcessImpl) Stop() error {
	return p.cmd.Process.Signal(syscall.SIGTERM)
}

// Gives access to basic nodes info, and to most avalanchego apis
type localNode struct {
	// Must be unique across all nodes in this network.
	name string
	// [nodeID] is this node's Avalannche Node ID.
	nodeID ids.ShortID
	// Allows user to make API calls to this node.
	client api.Client
	// The process running this node.
	process NodeProcess
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
