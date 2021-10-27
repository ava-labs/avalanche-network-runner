package network

import (
	"errors"

	"github.com/ava-labs/avalanche-network-runner-local/network/node"
)

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when the network is ready to be used for the first time,
	// and a chan that indicates if an error happened and the network will not be ready
	Ready() (chan struct{}, chan error)
	// Stop all the nodes
	Stop() error
	// Start a new node with the config
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this ID.
	RemoveNode(string) error
	// Return the node with this ID.
	GetNode(string) (node.Node, error)
	// Return ID for all the nodes
	GetNodesIDs() []string
	// TODO add methods
}

// Returns a new network whose initial state is specified in the config,
// using a map to set up proper node kind from integer kinds in config
func NewNetwork(Config, map[int]string) (*Network, error) {
	return nil, nil
}

type Config struct {
	NodeConfigs []node.Config // Node config for each node
}

func (c *Config) Validate() error {
	switch {
	case c.NodeConfigs == nil:
		return errors.New("nodeConfigs field is empty")
	case len(c.NodeConfigs) == 0:
		return errors.New("nodeConfigs field must have at least a node")
	default:
		return nil
	}
}
