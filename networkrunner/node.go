package networkrunner

import (
	"github.com/ava-labs/avalanchego/ids"
)

// kind of node to set up
// depending on node kind, proper binary/container will be executed
const (
	AVALANCHEGO = iota
	BYZANTINE   = iota
)

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
	GetAPIClient() APIClient
	// TODO add methods
}

type NodeConfig struct {
	BinKind     *uint  // Kind of node to set up (avalanchego/byzantine/...)
	NodeID      string // Avalanchego id for the node, when is known beforehand
	PrivateKey  string
	Cert        string
	ConfigFlags string // Cmdline flags that are specific for the node. JSON
}
