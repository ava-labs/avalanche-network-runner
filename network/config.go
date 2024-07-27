package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/formatting/address"
	"github.com/ava-labs/avalanchego/utils/units"
)

const validatorStake = units.MegaAvax

// AddrAndBalance holds both an address and its balance
type AddrAndBalance struct {
	Addr    ids.ShortID
	Balance *big.Int
}

// Config that defines a network when it is created.
type Config struct {
	// Must not be empty
	Genesis string `json:"genesis"`
	// If 0, will use default network ID
	NetworkID uint32 `json:"networkID"`
	// May have length 0
	// (i.e. network may have no nodes on creation.)
	NodeConfigs []node.Config `json:"nodeConfigs"`
	// Flags that will be passed to each node in this network.
	// It can be empty.
	// Config flags may also be passed in a node's config struct
	// or config file.
	// The precedence of flags handling is, from highest to lowest:
	// 1. Flags defined in a node's node.Config
	// 2. Flags defined in a network's network.Config
	// 3. Flags defined in a node's config file
	// For example, if a network.Config has flag W set to X,
	// and a node within that network has flag W set to Y,
	// and the node's config file has flag W set to Z,
	// then the node will be started with flag W set to Y.
	Flags map[string]interface{} `json:"flags"`
	// Binary path to use per default, if not specified in node config
	BinaryPath string `json:"binaryPath"`
	// Chain config files to use per default, if not specified in node config
	ChainConfigFiles map[string]string `json:"chainConfigFiles"`
	// Upgrade config files to use per default, if not specified in node config
	UpgradeConfigFiles map[string]string `json:"upgradeConfigFiles"`
	// Subnet config files to use per default, if not specified in node config
	SubnetConfigFiles map[string]string `json:"subnetConfigFiles"`
}

// Validate returns an error if this config is invalid
func (c *Config) Validate() error {
	if utils.IsCustomNetwork(c.NetworkID) && len(c.Genesis) == 0 {
		return errors.New("no genesis given")
	}

	var someNodeIsBeacon bool
	for i, nodeConfig := range c.NodeConfigs {
		if err := nodeConfig.Validate(c.NetworkID); err != nil {
			var nodeName string
			if len(nodeConfig.Name) > 0 {
				nodeName = nodeConfig.Name
			} else {
				nodeName = strconv.Itoa(i)
			}
			return fmt.Errorf("node %q config failed validation: %w", nodeName, err)
		}
		if nodeConfig.IsBeacon {
			someNodeIsBeacon = true
		}
	}
	if !utils.IsPublicNetwork(c.NetworkID) && len(c.NodeConfigs) > 0 && !someNodeIsBeacon {
		return errors.New("beacon nodes not given")
	}
	return nil
}

// Return a genesis JSON where:
// The nodes in [genesisVdrs] are validators.
// The C-Chain and X-Chain balances are given by
// [cChainBalances] and [xChainBalances].
// Note that many of the genesis fields (i.e. reward addresses)
// are randomly generated or hard-coded.
func NewAvalancheGoGenesis(
	networkID uint32,
	xChainBalances []AddrAndBalance,
	cChainBalances []AddrAndBalance,
	genesisVdrs []ids.NodeID,
) ([]byte, error) {
	switch networkID {
	case constants.TestnetID, constants.MainnetID, constants.LocalID:
		return nil, errors.New("network ID can't be mainnet, testnet or local network ID")
	}
	switch {
	case len(genesisVdrs) == 0:
		return nil, errors.New("no genesis validators provided")
	case len(xChainBalances)+len(cChainBalances) == 0:
		return nil, errors.New("no genesis balances given")
	}

	// Address that controls stake doesn't matter -- generate it randomly
	genesisVdrStakeAddr, _ := address.Format(
		"X",
		constants.GetHRP(networkID),
		ids.GenerateTestShortID().Bytes(),
	)
	config := genesis.UnparsedConfig{
		NetworkID: networkID,
		Allocations: []genesis.UnparsedAllocation{
			{
				ETHAddr:       "0x0000000000000000000000000000000000000000",
				AVAXAddr:      genesisVdrStakeAddr, // Owner doesn't matter
				InitialAmount: 0,
				UnlockSchedule: []genesis.LockedAmount{ // Provides stake to validators
					{
						Amount: uint64(len(genesisVdrs)) * validatorStake,
					},
				},
			},
		},
		StartTime:                  uint64(time.Now().Unix()),
		InitialStakedFunds:         []string{genesisVdrStakeAddr},
		InitialStakeDuration:       31_536_000, // 1 year
		InitialStakeDurationOffset: 5_400,      // 90 minutes
		Message:                    "hello world",
	}

	for _, xChainBal := range xChainBalances {
		xChainAddr, _ := address.Format("X", constants.GetHRP(networkID), xChainBal.Addr[:])
		config.Allocations = append(
			config.Allocations,
			genesis.UnparsedAllocation{
				ETHAddr:       "0x0000000000000000000000000000000000000000",
				AVAXAddr:      xChainAddr,
				InitialAmount: xChainBal.Balance.Uint64(),
				UnlockSchedule: []genesis.LockedAmount{
					{
						Amount:   validatorStake * uint64(len(genesisVdrs)), // Stake
						Locktime: uint64(time.Now().Add(7 * 24 * time.Hour).Unix()),
					},
				},
			},
		)
	}

	// Set initial C-Chain balances.
	cChainAllocs := map[string]interface{}{}
	for _, cChainBal := range cChainBalances {
		addrHex := fmt.Sprintf("0x%s", cChainBal.Addr.Hex())
		balHex := fmt.Sprintf("0x%x", cChainBal.Balance)
		cChainAllocs[addrHex] = map[string]interface{}{
			"balance": balHex,
		}
	}
	// avoid modifying original cChainConfig
	// localCChainConfig := maps.Clone(cChainConfig)
	// localCChainConfig["alloc"] = cChainAllocs
	// cChainConfigBytes, _ := json.Marshal(localCChainConfig)
	// config.CChainGenesis = string(cChainConfigBytes)

	// Set initial validators.
	// Give staking rewards to random address.
	rewardAddr, _ := address.Format("X", constants.GetHRP(networkID), ids.GenerateTestShortID().Bytes())
	for _, genesisVdr := range genesisVdrs {
		config.InitialStakers = append(
			config.InitialStakers,
			genesis.UnparsedStaker{
				NodeID:        genesisVdr,
				RewardAddress: rewardAddr,
				DelegationFee: 10_000,
			},
		)
	}

	// TODO add validation (from AvalancheGo's function validateConfig?)
	return json.Marshal(config)
}
