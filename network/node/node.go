package node

import (
	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	Type        interface{} // Kind of node to set up (avalanchego/byzantine/...)
	NodeID      string      // Avalanchego id for the node, when is known beforehand
	PrivateKey  string
	Cert        string
	ConfigFlags string // Cmdline flags that are specific for the node. JSON
}

// An AvalancheGo node
type Node interface {
	// Each node has a unique ID that distinguishes it from
	// other nodes in this network.
	// This is distinct from the Avalanche notion of a node ID.
	// This ID is assigned by the Network; it is not the hash
	// of a staking certificate.
	// We don't use the Avalanche node ID to reference nodes
	// because we may want to start a network where multiple nodes
	// have the same Avalanche node ID.
	GetID() ids.ID
	// Return this node's Avalanche node ID.
	GetNodeID() (ids.ShortID, error)
	// Return a client that can be used to
	GetAPIClient() api.Client
	// TODO add methods
}
