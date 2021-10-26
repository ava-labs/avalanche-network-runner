package networkrunner

import "github.com/ava-labs/avalanchego/ids"

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
	Genesis         string // Contents of genesis file for all nodes 
	CChainConfig    string // Contents of cchain config file for all nodes
	CoreConfigFlags string // Common cmdline flags for all nodes (unless overwritten with node config). JSON
	NodeConfigs     []NodeConfig // Node config for each node
}
