package node

import (
	"errors"
	"io"

	"github.com/ava-labs/avalanche-network-runner-local/api"
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
	// If nil, a unique staking key/cert is
	// assigned on node creation.
	// If nil, [StakingCert] must also be nil.
	StakingKey []byte
	// If nil, a unique staking key/cert is
	// assigned on node creation.
	// If nil, [StakingKey] must also be nil.
	StakingCert []byte
	// Must not be nil.
	ConfigFile []byte
	// May be nil.
	CChainConfigFile []byte
	// Must not be nil.
	GenesisFile []byte
	// TODO make the below specific to local network runner
	// If non-nil, direct this node's stdout here
	Stdout io.Writer
	// If non-nil, direct this node's stderr here
	Stderr io.Writer
}

// Returns an error if this config is invalid
func (c *Config) Validate() error {
	switch {
	case c.ImplSpecificConfig == nil:
		return errors.New("implementation-specific node config not given")
	case len(c.ConfigFile) == 0:
		return errors.New("node config not given")
	case len(c.GenesisFile) == 0:
		return errors.New("genesis file not given")
	case len(c.StakingKey) != 0 && len(c.StakingCert) == 0:
		return errors.New("staking key given but not staking cert")
	case len(c.StakingKey) == 0 && len(c.StakingCert) != 0:
		return errors.New("staking cert given but not staking key")
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
