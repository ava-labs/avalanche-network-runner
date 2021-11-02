package api

import (
	"context"
	"fmt"
	"strconv"
)

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when
	// all the nodes in the network are healthy.
	// If an error is sent on this channel, at least 1
	// node didn't become healthy before the timeout.
	// If an error isn't sent on the channel before it
	// closes, all the nodes are healthy.
	// A stopped network is considered unhealthy.
	Healthy() chan error
	// Stop all the nodes.
	// Calling Stop after the first call does nothing
	// and returns nil.
	Stop(context.Context) error
	// Start a new node with the given config.
	// Returns an error if Stop() was previously called.
	AddNode(NodeConfig) (Node, error)
	// Stop the node with this name.
	// Returns an error if Stop() was previously called.
	RemoveNode(name string) error
	// Return the node with this name.
	// Returns an error if Stop() was previously called.
	GetNode(name string) (Node, error)
	// Returns the names of all nodes in this network.
	// Returns nil if Stop() was previously called.
	GetNodesNames() []string
	// TODO add methods
}

type NetworkConfig struct {
	// How many nodes in the network
	NodeCount int
	// Config for each node
	NodeConfigs []NodeConfig
	// Log level for the whole network
	LogLevel string
	// Name for the network
	Name string
}

// TODO enforce that all nodes have same genesis.
func (c *NetworkConfig) Validate() error {
	for i, nodeConfig := range c.NodeConfigs {
		if err := nodeConfig.Validate(); err != nil {
			var nodeName string
			if len(nodeConfig.Name) > 0 {
				nodeName = nodeConfig.Name
			} else {
				nodeName = strconv.Itoa(i)
			}
			return fmt.Errorf("node %q config failed validation: %w", nodeName, err)
		}
	}
	return nil
}
