package networkrunner

import (
	"errors"

	"github.com/ava-labs/avalanchego/ids"
)

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when the network is ready to be used for the first time,
	// and a chan that indicates if an error happened and the network will not be ready
	Ready() (chan struct{}, chan error)
	// Stop all the nodes
	Stop() error
	// Start a new node with the config
	AddNode(NodeConfig) (Node, error)
	// Stop the node with this ID.
	RemoveNode(ids.ID) error
	// Return the node with this ID.
	GetNode(ids.ID) (Node, error)
	// Return ID for all the nodes
	GetNodesIDs() []ids.ID
	// TODO add methods
}

// Returns a new network whose initial state is specified in the config,
// using a map to set up proper node kind from integer kinds in config
func NewNetwork(NetworkConfig, map[int]string) (*Network, error) {
	return nil, nil
}

type NetworkConfig struct {
	Genesis         string       // Contents of genesis file for all nodes
	CChainConfig    string       // Contents of cchain config file for all nodes
	CoreConfigFlags string       // Common cmdline flags for all nodes (unless overwritten with node config). JSON
	NodeConfigs     []NodeConfig // Node config for each node
}

func (c *NetworkConfig) Validate() error {
	switch {
	case c.Genesis == "":
		return errors.New("genesis field is empty")
	case c.CChainConfig == "":
		return errors.New("cChainConfig field is empty")
	case c.CoreConfigFlags == "":
		return errors.New("coreConfigFlags field is empty")
	case c.NodeConfigs == nil:
		return errors.New("nodeConfigs field is empty")
	case len(c.NodeConfigs) == 0:
		return errors.New("nodeConfigs field must have at least a node")
	default:
		return nil
	}
}
