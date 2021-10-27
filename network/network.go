package network

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/config"
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
	Genesis         string        // Contents of genesis file for all nodes
	CChainConfig    string        // Contents of cchain config file for all nodes
	CoreConfigFlags string        // Common cmdline flags for all nodes (unless overwritten with node config). JSON
	NodeConfigs     []node.Config // Node config for each node
}

func (c *Config) Validate() error {
	var coreConfigFlags map[string]interface{}
	switch {
	case c.Genesis == "":
		return errors.New("Genesis field is empty")
	case c.CChainConfig == "":
		return errors.New("CChainConfig field is empty")
	case c.CoreConfigFlags == "":
		return errors.New("CoreConfigFlags field is empty")
	case c.NodeConfigs == nil:
		return errors.New("NodeConfigs field is empty")
	case len(c.NodeConfigs) == 0:
		return errors.New("NodeConfigs field must have at least a node")
	default:
		if err := json.Unmarshal([]byte(c.CoreConfigFlags), &coreConfigFlags); err != nil {
			return fmt.Errorf("couldn't unmarshal core config flags: %w", err)
		}
		_, ok := coreConfigFlags[config.PublicIPKey].(string)
		if !ok {
			return fmt.Errorf("no config flag %s", config.PublicIPKey)
		}
		for idx, nodeConfig := range c.NodeConfigs {
			if err := nodeConfig.Validate(); err != nil {
				return fmt.Errorf("config for node %v failed validation: %w", idx, err)
			}
		}
		return nil
	}
}
