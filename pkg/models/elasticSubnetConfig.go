package models

import (
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

type ElasticSubnetConfig struct {
	TxID                     ids.ID
	SubnetID                 ids.ID
	AssetID                  ids.ID
	InitialSupply            uint64
	MaxSupply                uint64
	MinConsumptionRate       uint64
	MaxConsumptionRate       uint64
	MinValidatorStake        uint64
	MaxValidatorStake        uint64
	MinStakeDuration         time.Duration
	MaxStakeDuration         time.Duration
	MinDelegationFee         uint32
	MinDelegatorStake        uint64
	MaxValidatorWeightFactor byte
	UptimeRequirement        uint32
}
