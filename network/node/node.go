package node

import (
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	Type             interface{} // Kind of node to set up (avalanchego/byzantine/...)
	Name             string      // Must be unique across all nodes
	StakingKey       []byte
	StakingCert      []byte
	ConfigFile       []byte
	CChainConfigFile []byte
	GenesisFile      []byte
	APIPort          uint // Must be the the same as one given in config file
}

// An AvalancheGo node
type Node interface {
	// Return this node's name, which is unique
	// across all the nodes in its network.
	GetName() string
	// Return this node's Avalanche node ID.
	GetNodeID() (ids.ShortID, error)
	// Return a client that can be used to make API calls.
	GetAPIClient() api.Client
	// TODO add methods
}
