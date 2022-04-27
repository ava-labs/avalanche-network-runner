// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

import (
	"context"
	"time"
)

// Node provides an interface to interact with a node on a given network
type Node interface {
	GetName() string
	Config() map[string]interface{}
	GetHTTPBaseURI() string
	GetBootstrapIP() string
	Stop(timeout time.Duration) error // TODO pass in [ctx] instead of [timeout]
}

// Network provides an interface for configuring Nodes
type Network interface {
	// GetName returns the name of the network
	GetName() string
	// GetNodes returns the names of each node in the network
	GetNodes() ([]Node, error)
	// GetNode returns the Node corresponding to [name]
	GetNode(name string) (Node, error)
	// AddNode adds new node to the network
	AddNode(ctx context.Context, config NodeConfig) (Node, error)
	// RemoveNode stops and removes the node from the network
	RemoveNode(name string, timeout time.Duration) error
	// Teardown stops the network and additionally tears down all of the resources associated with it
	Teardown(ctx context.Context) error
}

// NetworkOrchestrator provides an interface to orchestrate networks using an arbitrary backend
type NetworkOrchestrator interface {
	CreateNetwork(name string) (Network, error)
	GetNetworks() ([]Network, error)
	GetNetwork(name string) (Network, error)
	Teardown(ctx context.Context) error
}
