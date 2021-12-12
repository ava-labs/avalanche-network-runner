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
	// May be nil.
	ConfigFile []byte
	// May be nil.
	CChainConfigFile []byte
}
```

As you can see, some fields of the config must be set, while others will be auto-generated if not provided.
Bootstrap IPs/ IDs will be overwritten even if provided.


A node's configuration may include fields that are specific to the type of network runner being used (see `ImplSpecificConfig` in the struct above.)
For example, a node running in a Kubernetes cluster has a config field that specifies the Docker image that the node runs,
whereas a node running locally has a config field that specifies the path of the binary that the node runs.

## Genesis Generation

You can create a custom AvalancheGo genesis with function `network.NewAvalancheGoGenesis`:

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

## Network Creation

Each network runner implementation (local/Kubernetes) has a function `NewNetwork` that returns a new network,
parameterized on `network.Config`:

```go
type Config struct {
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

## Default Network Creation

The local network runner implementation includes a helper function `NewDefaultNetwork`, which returns a network using a pre-defined configuration.
This allows users to create a new network without needing to define any configurations. 

```go
// NewDefaultNetwork returns a new network using a pre-defined
// network configuration.
// The following addresses are pre-funded:
// X-Chain Address 1:     X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// X-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// X-Chain Address 2:     X-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// X-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
// P-Chain Address 1:     P-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p
// P-Chain Address 1 Key: PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN
// P-Chain Address 2:     P-custom16045mxr3s2cjycqe2xfluk304xv3ezhkhsvkpr
// P-Chain Address 2 Key: PrivateKey-2fzYBh3bbWemKxQmMfX6DSuL2BFmDSLQWTvma57xwjQjtf8gFq
// C-Chain Address:       0x8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC
// C-Chain Address Key:   56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027
// The following nodes are validators:
// * NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg
// * NodeID-MFrZFVCXPv5iCn6M9K6XduxGTYp891xXZ
// * NodeID-NFBbbJ4qCmNaCzeW7sxErhvWqvEQMnYcN
// * NodeID-GWPcbFJZFfZreETSoWjPimr846mXEKCtu
// * NodeID-P7oB2McjBGgW2NXXWVYjV8JEDFoW9xDE5
func NewDefaultNetwork(
	log logging.Logger,
	binaryPath string,
) (network.Network, error)
```

The associated pre-defined configuration is also available to users by calling `NewDefaultConfig` function.

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
git clone https://github.com/ava-labs/avalanche-network-runner.git
```

### Run Unit Tests

Inside the directory cloned above:

```sh
go test ./...
```

### Run an Example

As an example of how to use the network runner, we've included a `main.go` that uses the local implementation of the network runner.
When run, it:
* Creates a local five node Avalanche network and waits for all nodes to become healthy.
* Prints the names of the nodes
* Prints the node ID of one node
* Starts a new node
* Removes an existing node

The network runs until the user provides a SIGINT or SIGTERM.

It assumes:

1. You have the latest AvalancheGo binaries at `$GOPATH/src/github.com/ava-labs/avalanchego/build`. For instructions on setting up AvalancheGo, see [here.](https://github.com/ava-labs/avalanchego)
2. The network runner direcory is at `$GOPATH/src/github.com/ava-labs/avalanche-network-runner`.

To run the demo:

```sh
go run examples/local/indepth/main.go
```

We've also included another example at `examples/local/fivenodenetwork/main.go`, which just starts a local five node network, waits for the nodes to become healthy, and then runs until the user provides a SIGINT or SIGTERM.