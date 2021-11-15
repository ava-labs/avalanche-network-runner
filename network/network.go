package network

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanche-network-runner/network/node"
)

var ErrStopped = errors.New("network stopped")

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when
	// all the nodes in the network are healthy.
	// If an error is sent on this channel, at least 1
	// node didn't become healthy before the timeout.
	// If an error isn't sent on the channel before it
	// closes, all the nodes are healthy.
	// A stopped network is considered unhealthy.
	// Timeout is given by the context parameter.
	// [ctx] must eventually be cancelled -- if it isn't, a goroutine is leaked.
	Healthy(context.Context) chan error
	// Stop all the nodes.
	// Returns ErrStopped if Stop() was previously called.
	Stop(context.Context) error
	// Start a new node with the given config.
	// Returns ErrStopped if Stop() was previously called.
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	RemoveNode(name string) error
	// Return the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	GetNode(name string) (node.Node, error)
	// Returns the names of all nodes in this network.
	// Returns ErrStopped if Stop() was previously called.
	GetNodesNames() ([]string, error)
	// TODO add methods
}
