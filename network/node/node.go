package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/snow/networking/router"
)

// Node represents an AvalancheGo node
type Node interface {
	// Return this node's name, which is unique
	// across all the nodes in its network.
	GetName() string
	// Return this node's Avalanche node ID.
	GetNodeID() ids.ShortID
	// Return a client that can be used to make API calls.
	GetAPIClient() api.Client
	// Return this node's IP (e.g. 127.0.0.1).
	GetURL() string
	// Return this node's P2P (staking) port.
	GetP2PPort() uint16
	// Return this node's HTP API port.
	GetAPIPort() uint16
	// Starts a new test peer, connects it to the given node, and returns the peer.
	// [handler] defines how the test peer handles messages it receives.
	// The test peer can be used to send messages to the node it's attached to.
	// It's left to the caller to maintain a reference to the returned peer.
	// The caller should call StartClose() on the peer when they're done with it.
	AttachPeer(ctx context.Context, handler router.InboundHandler) (peer.Peer, error)
	GetBinaryPath() string
	GetDbDir() string
	GetLogsDir() string
}

// Config encapsulates an avalanchego configuration
type Config struct {
	// A node's name must be unique from all other nodes
	// in a network. If Name is the empty string, a
	// unique name is assigned on node creation.
	Name string `json:"name"`
	// True if other nodes should use this node
	// as a bootstrap beacon.
	IsBeacon bool `json:"isBeacon"`
	// Must not be nil.
	StakingKey string `json:"stakingKey"`
	// Must not be nil.
	StakingCert string `json:"stakingCert"`
	// May be nil.
	ConfigFile string `json:"configFile"`
	// May be nil.
	CChainConfigFile string `json:"cChainConfigFile"`
	// Flags can hold additional flags for the node.
	// It can be empty.
	// The precedence of flags handling is:
	// 1. Flags defined in node.Config (this struct) override
	// 2. Flags defined in network.Config override
	// 3. Flags defined in the json config file
	Flags map[string]interface{} `json:"flags"`
	// What type of node this is
	BinaryPath string `json:"binaryPath"`
	// If non-nil, direct this node's Stdout to os.Stdout
	RedirectStdout bool `json:"redirectStdout"`
	// If non-nil, direct this node's Stderr to os.Stderr
	RedirectStderr bool `json:"redirectStderr"`
}

// Validate returns an error if this config is invalid
func (c *Config) Validate(expectedNetworkID uint32) error {
	switch {
	case c.StakingKey == "":
		return errors.New("staking key not given")
	case c.StakingCert == "":
		return errors.New("staking cert not given")
	default:
		return validateConfigFile([]byte(c.ConfigFile), expectedNetworkID)
	}
}

// Returns an error if config file [configFile] is invalid.
// If len([configFile]) == 0, returns nil.
func validateConfigFile(configFile []byte, expectedNetworkID uint32) error {
	if len(configFile) == 0 {
		// No config file given. Skip.
		return nil
	}
	// Validate that values given in the config file,
	// if any, are the correct type.
	var configMap map[string]interface{}
	if err := json.Unmarshal(configFile, &configMap); err != nil {
		return fmt.Errorf("could not unmarshal config file: %w", err)
	}
	if networkIDIntf, ok := configMap[config.NetworkNameKey]; ok {
		networkID, ok := networkIDIntf.(float64)
		if !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.NetworkNameKey, networkIDIntf)
		}
		if uint32(networkID) != expectedNetworkID {
			return fmt.Errorf("config file network id %d differs from genesis network id %d", int(networkID), expectedNetworkID)
		}
	}
	if dbPathIntf, ok := configMap[config.DBPathKey]; ok {
		if _, ok := dbPathIntf.(string); !ok {
			return fmt.Errorf("wrong type for field %q in config expected string got %T", config.DBPathKey, dbPathIntf)
		}
	}
	if logsDirIntf, ok := configMap[config.LogsDirKey]; ok {
		if _, ok := logsDirIntf.(string); !ok {
			return fmt.Errorf("wrong type for field %q in config expected string got %T", config.LogsDirKey, logsDirIntf)
		}
	}
	if apiPortIntf, ok := configMap[config.HTTPPortKey]; ok {
		if _, ok := apiPortIntf.(float64); !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.HTTPPortKey, apiPortIntf)
		}
	}
	if p2pPortIntf, ok := configMap[config.StakingPortKey]; ok {
		if _, ok := p2pPortIntf.(float64); !ok {
			return fmt.Errorf("wrong type for field %q in config expected float64 got %T", config.StakingPortKey, p2pPortIntf)
		}
	}
	return nil
}
