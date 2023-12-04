// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package utils

import (
	"encoding/json"
	"time"

	coreth_params "github.com/ava-labs/coreth/params"
)

// difference between unlock schedule locktime and startime in original genesis
const (
	genesisLocktimeStartimeDelta    = 2836800
	hexa0Str                        = "0x0"
	defaultLocalCChainFundedAddress = "8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC"
	defaultLocalCChainFundedBalance = "0x295BE96E64066972000000"
	allocationCommonEthAddress      = "0xb3d82b1367d362de99ab59a658165aff520cbd4d"
)

func generateCustomCchainGenesis() ([]byte, error) {
	cChainGenesisMap := map[string]interface{}{}
	cChainGenesisMap["config"] = coreth_params.AvalancheLocalChainConfig
	cChainGenesisMap["nonce"] = hexa0Str
	cChainGenesisMap["timestamp"] = hexa0Str
	cChainGenesisMap["extraData"] = "0x00"
	cChainGenesisMap["gasLimit"] = "0x5f5e100"
	cChainGenesisMap["difficulty"] = hexa0Str
	cChainGenesisMap["mixHash"] = "0x0000000000000000000000000000000000000000000000000000000000000000"
	cChainGenesisMap["coinbase"] = "0x0000000000000000000000000000000000000000"
	cChainGenesisMap["alloc"] = map[string]interface{}{
		defaultLocalCChainFundedAddress: map[string]interface{}{
			"balance": defaultLocalCChainFundedBalance,
		},
	}
	cChainGenesisMap["number"] = hexa0Str
	cChainGenesisMap["gasUsed"] = hexa0Str
	cChainGenesisMap["parentHash"] = "0x0000000000000000000000000000000000000000000000000000000000000000"
	return json.Marshal(cChainGenesisMap)
}

func generateCustomGenesis(
	networkID uint32,
	walletAddr string,
	stakingAddr string,
	nodeIDs []string,
) ([]byte, error) {
	genesisMap := map[string]interface{}{}

	// cchain
	cChainGenesisBytes, err := generateCustomCchainGenesis()
	if err != nil {
		return nil, err
	}
	genesisMap["cChainGenesis"] = string(cChainGenesisBytes)

	// pchain genesis
	genesisMap["networkID"] = networkID
	startTime := time.Now().Unix()
	genesisMap["startTime"] = startTime
	initialStakers := []map[string]interface{}{}
	for _, nodeID := range nodeIDs {
		initialStaker := map[string]interface{}{
			"nodeID":        nodeID,
			"rewardAddress": walletAddr,
			"delegationFee": 1000000,
		}
		initialStakers = append(initialStakers, initialStaker)
	}
	genesisMap["initialStakeDuration"] = 31536000
	genesisMap["initialStakeDurationOffset"] = 5400
	genesisMap["initialStakers"] = initialStakers
	lockTime := startTime + genesisLocktimeStartimeDelta
	allocations := []interface{}{}
	alloc := map[string]interface{}{
		"avaxAddr":      walletAddr,
		"ethAddr":       allocationCommonEthAddress,
		"initialAmount": 300000000000000000,
		"unlockSchedule": []interface{}{
			map[string]interface{}{"amount": 20000000000000000},
			map[string]interface{}{"amount": 10000000000000000, "locktime": lockTime},
		},
	}
	allocations = append(allocations, alloc)
	alloc = map[string]interface{}{
		"avaxAddr":      stakingAddr,
		"ethAddr":       allocationCommonEthAddress,
		"initialAmount": 0,
		"unlockSchedule": []interface{}{
			map[string]interface{}{"amount": 10000000000000000, "locktime": lockTime},
		},
	}
	allocations = append(allocations, alloc)
	genesisMap["allocations"] = allocations
	genesisMap["initialStakedFunds"] = []interface{}{
		stakingAddr,
	}
	genesisMap["message"] = "{{ fun_quote }}"

	return json.MarshalIndent(genesisMap, "", " ")
}
