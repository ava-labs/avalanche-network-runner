// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package networks

import (
	"encoding/base64"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/config"
)

const (
	LocalNetworkName             = "local"
	HTTPHost                     = "" // XXX
	PeerListGossipFrequency      = "250ms"
	NetworkInitialReconnectDelay = "250ms"
	NetworkMaxReconnectDelay     = "250ms"
	HealthCheckFreq              = "2s"
	DefaultLogLevel              = "debug"
)

type InitialNetworkConfig struct {
	Nodes []backend.NodeConfig `json:"initialNodes"`
}

// CreateBasicLocalNodeConfig creates the map for the config of a basic node with the required fields to allow
// the local network to connect and report healthy quickly.
func CreateBasicLocalNodeConfig() map[string]interface{} {
	return map[string]interface{}{
		config.NetworkNameKey:                  LocalNetworkName,
		config.HTTPHostKey:                     HTTPHost,
		config.NetworkPeerListGossipFreqKey:    PeerListGossipFrequency,
		config.NetworkInitialReconnectDelayKey: NetworkInitialReconnectDelay,
		config.NetworkMaxReconnectDelayKey:     NetworkMaxReconnectDelay,
		config.HealthCheckFreqKey:              HealthCheckFreq,
		config.LogLevelKey:                     DefaultLogLevel,
	}
}

// CreateLocalNetworkConfig creates the initial network config for the default local network
func CreateLocalNetworkConfig(executable string) *InitialNetworkConfig {
	netConfig := &InitialNetworkConfig{Nodes: make([]backend.NodeConfig, 0, len(constants.LocalNetworkStakerIDs))}

	for i := 0; i < len(constants.LocalNetworkStakerKeys); i++ {
		nodeConfig := CreateBasicLocalNodeConfig()

		if i != 0 {
			nodeConfig[config.BootstrapIDsKey] = constants.Staker1NodeID
		}
		nodeConfig[config.HTTPPortKey] = 9650 + i*2
		nodeConfig[config.StakingPortKey] = 9651 + i*2
		nodeConfig[config.StakingKeyContentKey] = base64.StdEncoding.EncodeToString([]byte(constants.LocalNetworkStakerKeys[i]))
		nodeConfig[config.StakingCertContentKey] = base64.StdEncoding.EncodeToString([]byte(constants.LocalNetworkStakerCerts[i]))

		netConfig.Nodes = append(netConfig.Nodes, backend.NodeConfig{
			Name:       fmt.Sprintf("node%d", i),
			Executable: executable,
			Config:     nodeConfig,
			NodeID:     constants.LocalNetworkStakerIDs[i],
		})
	}

	return netConfig
}
