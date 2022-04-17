# Avalanche Network Runner

## Note

This tool is under heavy development and the documentation/code snippers below may vary slightly from the actual code in the repository.
Updates to the documentation may happen some time after an update to the codebase.
Nonetheless, this README should provide valuable information about using this tool.

## Overview

This is a tool to run and interact with a local Avalanche network.
This tool may be especially useful for development and testing.


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

### Run E2E tests

The E2E test checks `avalanche-network-runner` RPC communication and control. It starts a network against a fresh RPC
server and executes a set of query and control operations on it.

To start it, execute inside the cloned directory:

```sh
./scripts/tests.e2e.sh AVALANCHEGO_VERSION1 AVALANCHEGO_VERSION2
```

The E2E test checks wheter a node can be restarted with a different binary version. Provide two
different versions as arguments. For Example:

```sh
./scripts/tests.e2e.sh 1.7.8 1.7.9
```

#### `RUN_E2E` environment variable

To specify that the E2E test should be run with `go test`, set environment variable `RUN_E2E` to any non-empty value. 

This environment variable is correctly set when executing `./scripts/tests.e2e.sh`, but the user should consider 
setting it if trying to execute E2E tests without using that script.

## Using `avalanche-network-runner`

You can import this repository as a library in your Go program, but we recommend running `avalanche-network-runner` as a binary.
This creates an RPC server that you can send requests to in order to start a network, add nodes to the network, remove nodes from the network, restart nodes, etc.. You can make requests through the `avalanche-network-runner` command or by making API calls. Requests are "translated" into gRPC and sent to the server.

**Why does `avalanche-network-runner` need an RPC server?** `avalanche-network-runner` needs to provide complex workflows such as replacing nodes, restarting nodes, injecting fail points, etc.. The RPC server exposes basic operations to enable a separation of concerns such that one team develops a test framework, and the other writes test cases and controlling logic.

**Why gRPC?** The RPC server leads to more modular test components, and gRPC enables greater flexibility. The protocol buffer increases flexibility as we develop more complicated test cases. And gRPC opens up a variety of different approaches for how to write test controller (e.g., Rust). See [`rpcpb/rpc.proto`](./rpcpb/rpc.proto) for service definition.

**Why gRPC gateway?** [gRPC gateway](https://grpc-ecosystem.github.io/grpc-gateway/) exposes gRPC API via HTTP, without us writing any code. Which can be useful if a test controller writer does not want to deal with gRPC.

## Examples

```bash
# to install
cd ${HOME}/go/src/github.com/ava-labs/avalanche-network-runner
go install -v ./cmd/avalanche-network-runner
```

To start the server:

```bash
avalanche-network-runner server \
--log-level debug \
--port=":8080" \
--grpc-gateway-port=":8081"
```

Note that the above command will run until you stop it with `CTRL + C`. You should run further commands in a separate terminal.

To ping the server:

```bash
curl -X POST -k http://localhost:8081/v1/ping -d ''

# or
avalanche-network-runner ping \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To start a new Avalanche network with five nodes (a cluster):

```bash
# replace execPath with the path to AvalancheGo on your machine
curl -X POST -k http://localhost:8081/v1/control/start -d '{"execPath":"/Users/gyuho.lee/go/src/github.com/ava-labs/avalanchego/build/avalanchego","numNodes":5,"whitelistedSubnets":"24tZhrm8j8GCJRE9PomW8FaeqbgGS4UAQjJnqqn8pq5NwYSYV1","logLevel":"INFO"}'

# or
avalanche-network-runner control start \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--avalanchego-path ${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego \
--whitelisted-subnets="24tZhrm8j8GCJRE9PomW8FaeqbgGS4UAQjJnqqn8pq5NwYSYV1"
```

To wait for all the nodes in the cluster to become healthy:

```bash
curl -X POST -k http://localhost:8081/v1/control/health -d ''

# or
avalanche-network-runner control health \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To get the API endpoints of all nodes in the cluster:

```bash
curl -X POST -k http://localhost:8081/v1/control/uris -d ''

# or
avalanche-network-runner control uris \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To query the cluster status from the server:

```bash
curl -X POST -k http://localhost:8081/v1/control/status -d ''

# or
avalanche-network-runner control status \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To stream cluster status:

```bash
avalanche-network-runner control \
--request-timeout=3m \
stream-status \
--push-interval=5s \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

To remove (stop) a node:

```bash
curl -X POST -k http://localhost:8081/v1/control/removenode -d '{"name":"node5"}'

# or
avalanche-network-runner control remove-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node5
```

To restart a node (in this case, the one named `node1`):

```bash
# Note that you can restart the node with a different binary by providing
# a different execPath
curl -X POST -k http://localhost:8081/v1/control/restartnode -d '{"name":"node1","startRequest":{"execPath":"/tmp/avalanchego-v1.7.9/build/avalanchego",whitelistedSubnets:"",,"logLevel":"INFO"}}'

# or
avalanche-network-runner control restart-node \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1 \
--avalanchego-path /tmp/avalanchego-v1.7.9/build/avalanchego \
--whitelisted-subnets=""
```

AvalancheGo exposes a "test peer", which you can attach to a node.
(See [here](https://github.com/ava-labs/avalanchego/blob/master/network/peer/test_peer.go) for more information.)
You can send messages through the test peer to the node it is attached to.

To attach a test peer to a node (in this case, `node1`):

```bash
curl -X POST -k http://localhost:8081/v1/control/attachpeer -d '{"nodeName":"node1"}'

# or
avalanche-network-runner control attach-peer \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1
```

To send a chit message to the node through the test peer:

```bash
curl -X POST -k http://localhost:8081/v1/control/sendoutboundmessage -d '{"nodeName":"node1","peerId":"7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg","op":16,"bytes":"EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA="}'

# or
avalanche-network-runner control send-outbound-message \
--request-timeout=3m \
--log-level debug \
--endpoint="0.0.0.0:8080" \
--node-name node1 \
--peer-id "7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg" \
--message-op=16 \
--message-bytes-b64="EAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAKgAAAAPpAqmoZkC/2xzQ42wMyYK4Pldl+tX2u+ar3M57WufXx0oXcgXfXCmSnQbbnZQfg9XqmF3jAgFemSUtFkaaZhDbX6Ke1DVpA9rCNkcTxg9X2EcsfdpKXgjYioitjqca7WA=" \
--message-bytes-throttling false \
```

To terminate the cluster:

```bash
curl -X POST -k http://localhost:8081/v1/control/stop -d ''

# or
avalanche-network-runner control stop \
--log-level debug \
--endpoint="0.0.0.0:8080"
```

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
  GetNodeNames() ([]string, error)
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
