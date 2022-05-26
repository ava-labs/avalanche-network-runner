package network

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanche-network-runner/network/node"
)

var ErrStopped = errors.New("network stopped")

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns nil if all the nodes in the network are healthy.
	// A stopped network is considered unhealthy.
	// Timeout is given by the context parameter.
	Healthy(context.Context) error
	// Stop all the nodes.
	// Returns ErrStopped if Stop() was previously called.
	Stop(context.Context) error
	// Start a new node with the given config.
	// Returns ErrStopped if Stop() was previously called.
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	RemoveNode(ctx context.Context, name string) error
	// Return the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	GetNode(name string) (node.Node, error)
	// Return all the nodes in this network.
	// Node name --> Node.
	// Returns ErrStopped if Stop() was previously called.
	GetAllNodes() (map[string]node.Node, error)
	// Returns the names of all nodes in this network.
	// Returns ErrStopped if Stop() was previously called.
	GetNodeNames() ([]string, error)
}
