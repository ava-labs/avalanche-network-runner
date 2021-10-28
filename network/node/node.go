package node

import (
	"encoding/json"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/config"
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

func (n *Config) Validate() error {
	var configFlags map[string]interface{}
	if n.Type == nil {
		return fmt.Errorf("Type field is empty")
	}
	if n.ConfigFlags == "" {
		return fmt.Errorf("ConfigFlags field is empty")
	}
	if err := json.Unmarshal([]byte(n.ConfigFlags), &configFlags); err != nil {
		return fmt.Errorf("couldn't unmarshal config flags: %w", err)
	}
	_, ok := configFlags[config.ChainConfigDirKey].(string)
	if !ok {
		return fmt.Errorf("no config flag %s", config.ChainConfigDirKey)
	}
	_, ok = configFlags[config.GenesisConfigFileKey].(string)
	if !ok {
		return fmt.Errorf("no config flag %s", config.GenesisConfigFileKey)
	}
	usesEphemeralCert, ok := configFlags[config.StakingEphemeralCertEnabledKey].(bool)
	if !ok {
		usesEphemeralCert = false
	}
	if !usesEphemeralCert {
		if n.Cert == "" {
			return fmt.Errorf("Cert field is empty")
		}
		_, ok = configFlags[config.StakingCertPathKey].(string)
		if !ok {
			return fmt.Errorf("no config flag %s", config.StakingCertPathKey)
		}
		if n.PrivateKey == "" {
			return fmt.Errorf("PrivateKey field is empty")
		}
		_, ok = configFlags[config.StakingKeyPathKey].(string)
		if !ok {
			return fmt.Errorf("no config flag %s", config.StakingKeyPathKey)
		}
	}
	_, ok = configFlags[config.HTTPPortKey].(float64)
	if !ok {
		return fmt.Errorf("no config flag %s", config.HTTPPortKey)
	}
	return nil
}
