package node

import (
	"io"

	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	// Kind of node to set up (avalanchego/byzantine/...)
	Type interface{}
	// Must be unique across all nodes
	Name             string
	StakingKey       []byte
	StakingCert      []byte
	ConfigFile       []byte
	CChainConfigFile []byte
	GenesisFile      []byte
	// TODO make the below specific to local network runner
	// If non-nil, direct this node's stdout here
	Stdout io.Writer
	// If non-nil, direct this node's stderr here
	Stderr io.Writer
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
