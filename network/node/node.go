package node

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	// Configuration specific to a particular implementation of a node.
	ImplSpecificConfig interface{}
	// A node's name must be unique from all other nodes
	// in a network. If Name is the empty string, a
	// unique name is assigned on node creation.
	Name string `json:"name"`
	// True if other nodes should use this node
	// as a bootstrap beacon.
	IsBeacon bool
	// Must not be nil.
	StakingKey []byte
	// Must not be nil.
	StakingCert []byte
	// May be nil.
	ConfigFile []byte
	// May be nil.
	CChainConfigFile []byte
}

// Returns an error if this config is invalid
func (c *Config) Validate(genesisNetworkID uint32) error {
	var configFile map[string]interface{}
	if len(c.ConfigFile) != 0 {
		if err := json.Unmarshal(c.ConfigFile, &configFile); err != nil {
			return fmt.Errorf("could not unmarshall config file: %w", err)
		}
	}
	networkIDIntf, ok := configFile[config.NetworkNameKey]
	if ok {
		networkID, ok := networkIDIntf.(float64)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.NetworkNameKey, networkIDIntf)
		}
		if uint32(networkID) != genesisNetworkID {
			return fmt.Errorf("config file network id %v differs from genesis network id %v", networkID, genesisNetworkID)
		}
	}
	dbPathIntf, ok := configFile[config.DBPathKey]
	if ok {
		_, ok = dbPathIntf.(string)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected string got %T", config.DBPathKey, dbPathIntf)
		}
	}
	logsDirIntf, ok := configFile[config.LogsDirKey]
	if ok {
		_, ok = logsDirIntf.(string)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected string got %T", config.LogsDirKey, logsDirIntf)
		}
	}
	apiPortIntf, ok := configFile[config.HTTPPortKey]
	if ok {
		_, ok := apiPortIntf.(float64)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.HTTPPortKey, apiPortIntf)
		}
	}
	p2pPortIntf, ok := configFile[config.StakingPortKey]
	if ok {
		_, ok := p2pPortIntf.(float64)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.StakingPortKey, p2pPortIntf)
		}
	}
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
