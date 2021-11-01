package network

import (
	"fmt"
	"strconv"

	"github.com/ava-labs/avalanche-network-runner-local/network/node"
)

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when
	// all the nodes in the network are healthy.
	// If an error is sent on this channel, at least 1
	// node didn't become healthy before the timeout.
	// If an error isn't sent on the channel before it
	// closes, all the nodes are healthy.
	Healthy() chan error
	// Stop all the nodes.
	// Calling Stop after the first call does nothing.
	Stop() error
	// Start a new node with the given config
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	RemoveNode(name string) error
	// Return the node with this name.
	GetNode(name string) (node.Node, error)
	// Returns the names of all nodes in this network.
	GetNodesNames() []string
	// TODO add methods
}

type Config struct {
	// Config for each node
	NodeConfigs []node.Config
}

// TODO enforce that all nodes have same genesis.
func (c *Config) Validate() error {
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
