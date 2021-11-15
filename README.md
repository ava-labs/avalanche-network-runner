# Avalanche Network Runner

## Overview

This is a tool to run and interact with an Avalanche network.
The nodes that compose the network can run either locally or in a Kubernetes cluster.
This tool may be especially useful for development and testing.

The network you run can be:

1. Locally: each node runs in a process on your computer.
2. In Kubernetes: each node runs in a Kubernetes pod. The Kubernetes pods may be on your computer or remote.

## Configuration

When the user creates a network, they specify the configurations of the nodes that are in the network upon creation.

A node config is defined by this struct:

```go
type Config struct {
	// Configuration specific to a particular implementation of a node.
	ImplSpecificConfig interface{}
	// A node's name must be unique from all other nodes
	// in a network. If Name is the empty string, a
	// unique name is assigned on node creation.
	Name string
	// True if other nodes should use this node
	// as a bootstrap beacon.
	IsBeacon bool
	// Must not be nil
	StakingKey []byte
	// Must not be nil
	StakingCert []byte
	// Must not be empty
	NodeID ids.ShortID
	// May be nil.
	ConfigFile []byte
	// May be nil.
	CChainConfigFile []byte
}
```

As you can see, some fields of the config must be set, while others will be auto-generated if not provided.
The following node configuration fields will be overwritten, even if provided:

- API port
- P2P (staking) port
- Paths to files such as the genesis, node config, etc.
- Log/database directories
- Bootstrap IPs/IDs (the user specifies which nodes are beacons, but doesn't directly provide bootstrap IPs/IDs.)
- Network ID (any network id info in avalanchego confs will be overwritten by the user specified network id at network conf)

A node's configuration may include fields that are specific to the type of network runner being used (see `ImplSpecificConfig` in the struct above.)
For example, a node running in a Kubernetes cluster has a config field that specifies the Docker image that the node runs,
whereas a node running locally has a config field that specifies the path of the binary that the node runs.

## Genesis Generation

Given network id, desired genesis balances, and validators, automatic genesis generation 
can be obtained by using `network.NewAvalancheGoGenesis`:

```go
// Return a genesis JSON where:
// The nodes in [genesisVdrs] are validators.
// The C-Chain and X-Chain balances are given by
// [cChainBalances] and [xChainBalances].
// Note that many of the genesis fields (i.e. reward addresses)
// are randomly generated or hard-coded.
func NewAvalancheGoGenesis(
	log logging.Logger,
	networkID uint32,
	xChainBalances []AddrAndBalance,
	cChainBalances []AddrAndBalance,
	genesisVdrs []ids.ShortID,
) ([]byte, error)
```

Later on the genesis contents can be used in network creation.

Note that both genesis and network conf should contain the same network id.

## Network Creation

Each network runner implementation (local/Kubernetes) has a function that returns a new network.

Each is parameterized on `network.Config`:

```go
type Config struct {
	// Must not be the ID of Mainnet, Testnet or Localnet.
	// If any nodes are given a config file, the network ID
	// in the config file will be over-ridden by this network ID.
	// This network ID must match the one in [Genesis].
	// TODO what if network ID here doesn't match that in genesis?
	NetworkID uint32
	// Configuration specific to a particular implementation of a network.
	ImplSpecificConfig interface{}
	// Must not be nil
	Genesis []byte
	// May have length 0
	// (i.e. network may have no nodes on creation.)
	NodeConfigs []node.Config
	// Log level for the whole network
	LogLevel string
	// Name for the network
	Name string
	// How many nodes in the network.
	// TODO move to k8s package?
	NodeCount int
}
```

The function that returns a new network may have additional configuration fields.

## Network Interaction

The network runner allows users to interact with an AvalancheGo network using the `network.Network` interface:

```go
// Network is an abstraction of an Avalanche network
type Network interface {
	// Returns a chan that is closed when
	// all the nodes in the network are healthy.
	// If an error is sent on this channel, at least 1
	// node didn't become healthy before the timeout.
	// If an error isn't sent on the channel before it
	// closes, all the nodes are healthy.
	// A stopped network is considered unhealthy.
	// Timeout is given by the context parameter.
	// [ctx] must eventually be cancelled -- if it isn't, a goroutine is leaked.
	Healthy(context.Context) chan error
	// Stop all the nodes.
	// Returns ErrStopped if Stop() was previously called.
	Stop(context.Context) error
	// Start a new node with the given config.
	// Returns ErrStopped if Stop() was previously called.
	AddNode(node.Config) (node.Node, error)
	// Stop the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	RemoveNode(name string) error
	// Return the node with this name.
	// Returns ErrStopped if Stop() was previously called.
	GetNode(name string) (node.Node, error)
	// Returns the names of all nodes in this network.
	// Returns ErrStopped if Stop() was previously called.
	GetNodesNames() ([]string, error)
	// TODO add methods
}
```

and allows users to interact with a node using the `node.Node` interface:

```go
// An AvalancheGo node
type Node interface {
    // Return this node's name, which is unique
    // across all the nodes in its network.
    GetName() string
    // Return this node's Avalanche node ID.
    GetNodeID() ids.ShortID
    // Return a client that can be used to make API calls.
    GetAPIClient() api.Client
}
```

## Installation

### Download

```sh
`git clone https://github.com/ava-labs/avalanche-network-runner.git`
```

### Run Unit Tests

Inside the directory cloned above:

```sh
go test ./...
```

### Run a Small Example

As an example of how to use the network runner, we've included a small `main.go` that uses the local implementation of the network runner.
When run, it starts a local network with 5 nodes, waits until the nodes are healthy, prints their names, then stops all the nodes.

It assumes:

1. You have the AvalancheGo v1.6.4 binaries at `$GOPATH/src/github.com/ava-labs/avalanchego/build`
2. The network runner direcory is at `$GOPATH/src/github.com/ava-labs/avalanche-network-runner`)

To run the demo:

```sh
go run examples/local/main.go
```
