package networkrunner

import "github.com/ava-labs/avalanchego/ids"

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when the network is ready to be used for the first time
	Ready() (chan struct{}, chan error)
	// Stop all the nodes
	Stop() error
	// Start a new node with the config
	//AddNode(NodeConfig) (Node, error)
	// Stop the node with this ID.
	//RemoveNode(ids.ID) error
	// Return the node with this ID.
	GetNode(ids.ID) (Node, error)
	// TODO add methods
}

// Returns a new network whose initial state is specified in the config
func NewNetwork(NetworkConfig, map[int]string) (*Network, error) {
	return nil, nil
}

type NetworkConfig struct {
	Genesis         []byte
	CChainConfig    []byte
	CoreConfigFlags []byte
	NodeConfigs     []NodeConfig
}
