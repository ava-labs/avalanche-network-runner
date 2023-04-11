package network

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanche-network-runner/network/node"
)

var (
	ErrUndefined    = errors.New("undefined network")
	ErrStopped      = errors.New("network stopped")
	ErrNodeNotFound = errors.New("node not found in network")
)

type ElasticSubnetSpec struct {
	SubnetID *string
	AssetName string
	InitialSupply uint64
	MaxSupply uint64
	MinConsumptionRate uint64
	MaxConsumptionRate uint64
	MinValidatorRate uint64
	MaxValidatorRate uint64
	MinStakeDuration uint64
	MaxStakeDuration uint64
	MinDelegationFee uint64
	MinDelegatorStake uint64
	MaxValidatorRate uint64

	uint64 initial_supply = 3;
	uint64 max_supply = 4;
	uint64 min_consumption_rate = 5;
	uint64 max_consumption_rate = 6;
	uint64 min_validator_rate = 7;
	uint64 max_validator_rate = 8;
	uint64 min_stake_duration = 9;
	uint64 max_stake_duration = 10;
	uint32 min_delegation_fee = 11;
	uint64 min_delegator_stake = 12;
	uint32 max_validator_weight_factor = 13;
	uint32 uptime_requirement = 14;
}

type SubnetSpec struct {
	Participants []string
	SubnetConfig []byte
}

type BlockchainSpec struct {
	VMName             string
	Genesis            []byte
	SubnetID           *string
	SubnetSpec         *SubnetSpec
	ChainConfig        []byte
	NetworkUpgrade     []byte
	BlockchainAlias    string
	PerNodeChainConfig map[string][]byte
}

// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns nil if all the nodes in the network are healthy.
	// A stopped network is considered unhealthy.
	// Timeout is given by the context parameter.
	Healthy(context.Context) error
	// Stop all the nodes.
	// Returns ErrStopped if Stop() was previously called.
	Stop(context.Context) error
	// Start a new node with the given config.
	// Returns ErrStopped if Stop() was previously called.
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	RemoveNode(ctx context.Context, name string) error
	// Pause the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	PauseNode(ctx context.Context, name string) error
	// Resume the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	ResumeNode(ctx context.Context, name string) error
	// Return the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	GetNode(name string) (node.Node, error)
	// Return all the nodes in this network.
	// Node name --> Node.
	// Returns ErrStopped if Stop() was previously called.
	GetAllNodes() (map[string]node.Node, error)
	// Returns the names of all nodes in this network.
	// Returns ErrStopped if Stop() was previously called.
	GetNodeNames() ([]string, error)
	// Save network snapshot
	// Network is stopped in order to do a safe preservation
	// Returns the full local path to the snapshot dir
	SaveSnapshot(context.Context, string) (string, error)
	// Remove network snapshot
	RemoveSnapshot(string) error
	// Get name of available snapshots
	GetSnapshotNames() ([]string, error)
	// Restart a given node using the same config, optionally changing binary path, plugin dir,
	// track subnets, a map of chain configs, a map of upgrade configs, and
	// a map of subnet configs
	RestartNode(context.Context, string, string, string, string, map[string]string, map[string]string, map[string]string) error
	// Create the specified blockchains
	CreateBlockchains(context.Context, []BlockchainSpec) error
	// Create the given numbers of subnets
	CreateSubnets(context.Context, []SubnetSpec) error
}
