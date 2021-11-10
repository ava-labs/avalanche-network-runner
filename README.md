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
    // If nil, a unique staking key/cert is
    // assigned on node creation.
    // If nil, [StakingCert] must also be nil.
    StakingKey []byte
    // If nil, a unique staking key/cert is
    // assigned on node creation.
    // If nil, [StakingKey] must also be nil.
    StakingCert []byte
    // Must not be nil.
    ConfigFile []byte
    // May be nil.
    CChainConfigFile []byte
    // Must not be nil.
    GenesisFile []byte
}
```

As you can see, some fields of the config must be set, while others will be auto-generated if not provided.
The following node configuration fields will be overwritten, even if provided:

- API port
- P2P (staking) port
- Paths to files such as the genesis, node config, etc.
- Log/database directories
- Bootstrap IPs/IDs (the user specifies which nodes are beacons, but doesn't directly provide bootstrap IPs/IDs.)

A node's configuration may include fields that are specific to the type of network runner being used (see `ImplSpecificConfig` in the struct above.)
For example, a node running in a Kubernetes cluster has a config field that specifies the Docker image that the node runs,
whereas a node running locally has a config field that specifies the path of the binary that the node runs.

## Network Creation

Each network runner implementation (local/Kubernetes) has a function that returns a new network.

Each is parameterized on `network.Config`:

```go
type Config struct {
   // How many nodes are the network
   NodeCount int
   // Config for each node
   NodeConfigs []node.Config
   // Log level for the whole network
   LogLevel string
   // Name for the network
   Name string
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
    Healthy() chan error
    // Stop all the nodes.
    // Calling Stop after the first call does nothing
    // and returns nil.
    Stop(context.Context) error
    // Start a new node with the given config.
    // Returns an error if Stop() was previously called.
    AddNode(node.Config) (node.Node, error)
    // Stop the node with this name.
    // Returns an error if Stop() was previously called.
    RemoveNode(name string) error
    // Return the node with this name.
    // Returns an error if Stop() was previously called.
    GetNode(name string) (node.Node, error)
    // Returns the names of all nodes in this network.
    // Returns nil if Stop() was previously called.
    GetNodesNames() []string
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
