package network

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanche-network-runner/network/node"
)

var (
	ErrUndefined    = errors.New("undefined network")
	ErrStopped      = errors.New("network stopped")
	ErrNodeNotFound = errors.New("node not found in network")
)

type BlockchainSpec struct {
	VmName   string
	Genesis  []byte
	SubnetId *string
}

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
	// Save network snapshot
	// Network is stopped in order to do a safe preservation
	// Returns the full local path to the snapshot dir
	SaveSnapshot(context.Context, string) (string, error)
	// Remove network snapshot
	RemoveSnapshot(string) error
	// Get name of available snapshots
	GetSnapshotNames() ([]string, error)
	// Restart a given node using the same config, optionally changing binary path,
	// whitelisted subnets
	RestartNode(context.Context, string, string, string) error
	// Create the specified blockchains
	CreateBlockchains(context.Context, []BlockchainSpec) error
	// Create the given numbers of subnets
	CreateSubnets(context.Context, uint32) error
}
