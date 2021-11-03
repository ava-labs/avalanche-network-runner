package network

import (
	"fmt"
	"strconv"
    "bytes"

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
	// A stopped network is considered unhealthy.
	Healthy() chan error
	// Stop all the nodes.
	// Calling Stop after the first call does nothing
	// and returns nil.
	Stop() error
	// Start a new node with the given config.
	// Returns an error if Stop() was previously called.
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	// Returns an error if Stop() was previously called.
	RemoveNode(name string) error
	// Return the node with this name.
	// Returns an error if Stop() was previously called.
	GetNode(name string) (node.Node, error)
	// Returns the names of all nodes in this network.
	// Returns nil if Stop() was previously called.
	GetNodesNames() []string
	// TODO add methods
}

type Config struct {
	// Config for each node
	NodeConfigs []node.Config
}

// TODO current implementation requires first node to be beacon, instead of some one, like the validation enforces
func (c *Config) Validate() error {
    var genesisFile []byte
    var firstNodeName string
    var hasSomeBeacon bool
	for i, nodeConfig := range c.NodeConfigs {
        var nodeName string
        if len(nodeConfig.Name) > 0 {
            nodeName = nodeConfig.Name
        } else {
            nodeName = strconv.Itoa(i)
        }
        if nodeConfig.IsBeacon {
            hasSomeBeacon = true
        }
        if i == 0 {
            genesisFile = nodeConfig.GenesisFile
            firstNodeName = nodeName
        } else {
            if bytes.Compare(genesisFile, nodeConfig.GenesisFile) != 0 {
			    return fmt.Errorf("node %q config failed validation: genesis file difference against %q", nodeName, firstNodeName)
            }
        }
		if err := nodeConfig.Validate(); err != nil {
			return fmt.Errorf("node %q config failed validation: %w", nodeName, err)
		}
	}
    if len(c.NodeConfigs) != 0 && !hasSomeBeacon {
			return fmt.Errorf("node configs failed validation: there are 0 beacon nodes")
    }
	return nil
}
