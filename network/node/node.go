package node

import (
	"errors"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	// Configuration specific to a particular implementation of a node.
	ImplSpecificConfig interface{}
	// A node's name must be unique from all other nodes
	// in a network. If Name is the empty string, a
	// unique name is assigned on node creation.
	Name string
	// True if other nodes should use this node
	// as a bootstrap beacon.
	IsBeacon bool
	// Must not be nil
	StakingKey []byte
	// Must not be nil
	StakingCert []byte
	// Must not be nil
	NodeID ids.ShortID
	// Must not be nil.
	// TODO what if network ID here doesn't match that in genesis?
	ConfigFile []byte
	// May be nil.
	CChainConfigFile []byte
}

// Returns an error if this config is invalid
func (c *Config) Validate() error {
	switch {
	case c.ImplSpecificConfig == nil:
		return errors.New("implementation-specific node config not given")
	case len(c.StakingKey) == 0:
		return errors.New("staking key not given")
	case len(c.StakingCert) == 0:
		return errors.New("staking cert not given")
	default:
		return nil
	}
}

// An AvalancheGo node
type Node interface {
	// Return this node's name, which is unique
	// across all the nodes in its network.
	GetName() string
	// Return this node's Avalanche node ID.
	GetNodeID() ids.ShortID
	// Return a client that can be used to make API calls.
	GetAPIClient() api.Client
	// TODO add methods
}
