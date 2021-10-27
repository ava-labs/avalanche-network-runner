package node

import (
	"encoding/json"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/network/node/api"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/ids"
)

type Config struct {
	Type        interface{} // Kind of node to set up (avalanchego/byzantine/...)
	NodeID      string      // Avalanchego id for the node, when is known beforehand
	Name        string      // Network runner node id for the node, when specified
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
	GetID() string
	// Return this node's Avalanche node ID.
	GetNodeID() (ids.ShortID, error)
	// Return a client that can be used to
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
