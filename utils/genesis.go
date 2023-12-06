// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	coreth_params "github.com/ava-labs/coreth/params"
)

// difference between unlock schedule locktime and startime in original genesis
const (
	genesisLocktimeStartimeDelta    = 2836800
	hexa0Str                        = "0x0"
	defaultLocalCChainFundedAddress = "8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC"
	defaultLocalCChainFundedBalance = "0x295BE96E64066972000000"
	allocationCommonEthAddress      = "0xb3d82b1367d362de99ab59a658165aff520cbd4d"
	stakingAddr                     = "X-custom1g65uqn6t77p656w64023nh8nd9updzmxwd59gh"
	walletAddr                      = "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p"
)

func generateCchainGenesis() ([]byte, error) {
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

func GenerateGenesis(
	netConfig network.Config,
) ([]byte, error) {
	genesisMap := map[string]interface{}{}

	// cchain
	cChainGenesisBytes, err := generateCchainGenesis()
	if err != nil {
		return nil, err
	}
	genesisMap["cChainGenesis"] = string(cChainGenesisBytes)

	// pchain genesis
	genesisMap["networkID"] = netConfig.NetworkID
	startTime := time.Now().Unix()
	genesisMap["startTime"] = startTime
	initialStakers := []map[string]interface{}{}

	for _, nodeConfig := range netConfig.NodeConfigs {
		nodeID, err := ToNodeID([]byte(nodeConfig.StakingKey), []byte(nodeConfig.StakingCert))
		if err != nil {
			return nil, fmt.Errorf("couldn't get node ID: %w", err)
		}
		blsKeyBytes, err := base64.StdEncoding.DecodeString(nodeConfig.StakingSigningKey)
		if err != nil {
			return nil, err
		}
		blsSk, err := bls.SecretKeyFromBytes(blsKeyBytes)
		if err != nil {
			return nil, err
		}
		p := signer.NewProofOfPossession(blsSk)
		pk, err := formatting.Encode(formatting.HexNC, p.PublicKey[:])
		if err != nil {
			return nil, err
		}
		pop, err := formatting.Encode(formatting.HexNC, p.ProofOfPossession[:])
		if err != nil {
			return nil, err
		}
		initialStaker := map[string]interface{}{
			"delegationFee": 1000000,
			"nodeID":        nodeID,
			"rewardAddress": walletAddr,
			"signer": map[string]interface{}{
				"proofOfPossession": pop,
				"publicKey":         pk,
			},
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
