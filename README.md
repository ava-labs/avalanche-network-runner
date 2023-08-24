# Avalanche Network Runner

The Avalanche Network Runner (ANR) is a tool to run and interact with a local Avalanche network. An Avalanche network is a collection of nodes utilizing the Avalanche consensus mechanism to come to agreement on the blockchains of the Primary Network and Subnets hosted on the network.

Each Avalanche network has its own Primary Network, which consists of the Contract (C), Platform (P), and Exchange (X) chain. Your local network is completely independent from the Mainnet or Fuji Testnet. You can even run your own Avalanche Network without being connected to the internet! Any local Avalanche network supports (but is not limited to) the following commands:

- **Start and stop a network**: Starts and stops a local network with a specified number of nodes
- **Add, remove and stop a node**: Give control to change the set of nodes of the network
- **Health Check**: Provide information on the health of each node of the network
- **Save and Load Snapshots**: Save the current state of each node into a snapshot
- **Create Subnets**: Create a new Subnet validated by a specified subset of the nodes
- **Create Blockchains**: Create a new Blockchain as part of a new or existing Subnet

When we start a network, create a new Subnet, or add a blockchain to a Subnet, we will need to communicate with all the nodes involved. Since our local network may consist of many nodes, this can take a lot of effort.

To make managing the local Avalanche network less tedious, the Avalanche Network Runner introduces a gRPC server that manages the nodes for us. Therefore, we can just tell the gRPC what we would like to do, an example being to create a new Subnet, and it will coordinate the nodes accordingly. This way we can interact with one gRPC Server instead of managing all 5 nodes individually.

![Architecture diagram](/docs/assets/diagram.png)

## Usage

First of all, start the Avalanche Network Runner Server.

Secondly, interact with it.

There are two ways you can interact with the server:

- **Command Line**: Command can be issued using the command line, e.g. `avalanche-network-runner control stop`
- **HTTP**: You can also send command as HTTP Post requests to the Avalanche Network Runner. Requests can be made via curl or via a tool such as the [Avalanche Network Runner Postman Collection](https://github.com/ava-labs/avalanche-network-runner-postman-collection).

While the command line is handy for short commands (e.g. stopping the network), issuing more complex commands with more data, like adding a blockchain, can be hard from the command line. Therefore, we recommend using the HTTP endpoints for that. Both ways can be combined.

## Installation

To download a binary for the latest release, run:

```sh
curl -sSfL https://raw.githubusercontent.com/ava-labs/avalanche-network-runner/main/scripts/install.sh | sh -s
```

To install a specific version, just append the desired version to the command (must be an existing github tag like v1.7.1)

```sh
curl -sSfL https://raw.githubusercontent.com/ava-labs/avalanche-network-runner/main/scripts/install.sh | sh -s v1.7.1
```

The binary will be installed inside the `~/bin` directory.

To add the binary to your path, run

```sh
export PATH=~/bin:$PATH
```

To add it to your path permanently, add an export command to your shell initialization script (ex: .bashrc).

## Build from source code

This is only needed by advanced users who want to modify or test Avalanche Network Runner in specific ways.

Requires golang to be installed on the system ([https://go.dev/doc/install](https://go.dev/doc/install)).

### Clone the Repo

```sh
git clone https://github.com/ava-labs/avalanche-network-runner.git
cd avalanche-network-runner/
```

### Build

From inside the cloned directory:

```sh
./scripts/build.sh
```

The binary will be installed inside the `./bin` directory.

To add the binary to your path, run

```sh
export PATH=$PWD/bin:$PATH
```

Pass in a path to have the binary installed in a different location than `./bin`.

```sh
./scripts/build.sh build
```

In this example the binary will be installed inside the `./build` directory.

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
./scripts/tests.e2e.sh
```

## Using `avalanche-network-runner`

You can import this repository as a library in your Go program, but we recommend running `avalanche-network-runner` as a binary. This creates an RPC server that you can send requests to in order to start a network, add nodes to the network, remove nodes from the network, restart nodes, etc.. You can make requests through the `avalanche-network-runner` command or by making API calls. Requests are "translated" into gRPC and sent to the server. Requests can be made via curl or via a tool such as the [Avalanche Network Runner Postman Collection](https://github.com/ava-labs/avalanche-network-runner-postman-collection).

### Using default paths

ANR needs to know the location of the avalanchego binary and the vm plugins that will be used by the network. It is recommended that you export the environment variables `AVALANCHEGO_EXEC_PATH` and `AVALANCHEGO_PLUGIN_PATH` before starting the server. They will be used as default when starting networks and creating blockchains if no command line flags are passed.

Example setting:

```sh
export AVALANCHEGO_EXEC_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/avalanchego"
export AVALANCHEGO_PLUGIN_PATH="${HOME}/go/src/github.com/ava-labs/avalanchego/build/plugins"
```

### Starting and pinging the server

To start the server:

```sh
avalanche-network-runner server
```

**Note** that the above command will run until you stop it with `CTRL + C`. You should run further commands in a separate terminal.

To ping the server:

```sh
avalanche-network-runner ping

# or
curl -X POST -k http://localhost:8081/v1/ping
```

### Starting a default network

To start a new Avalanche network with five nodes:

```sh
avalanche-network-runner control start

# or
curl -X POST -k http://localhost:8081/v1/control/start 
```

## Configuration

When the user creates a network, they specify the configurations of the nodes that are in the network upon creation.

A node config is defined by this struct:

```go
type Config struct {
  // A node's name must be unique from all other nodes
  // in a network. If Name is the empty string, a
  // unique name is assigned on node creation.
  Name string `json:"name"`
  // True if other nodes should use this node
  // as a bootstrap beacon.
  IsBeacon bool `json:"isBeacon"`
  // Must not be nil.
  StakingKey string `json:"stakingKey"`
  // Must not be nil.
  StakingCert string `json:"stakingCert"`
  // Must not be nil.
  StakingSigningKey string `json:"stakingSigningKey"`
  // May be nil.
  ConfigFile string `json:"configFile"`
  // May be nil.
  ChainConfigFiles map[string]string `json:"chainConfigFiles"`
  // May be nil.
  UpgradeConfigFiles map[string]string `json:"upgradeConfigFiles"`
  // May be nil.
  SubnetConfigFiles map[string]string `json:"subnetConfigFiles"`
  // Flags can hold additional flags for the node.
  // It can be empty.
  // The precedence of flags handling is:
  // 1. Flags defined in node.Config (this struct) override
  // 2. Flags defined in network.Config override
  // 3. Flags defined in the json config file
  Flags map[string]interface{} `json:"flags"`
  // What type of node this is
  BinaryPath string `json:"binaryPath"`
  // If non-nil, direct this node's Stdout to os.Stdout
  RedirectStdout bool `json:"redirectStdout"`
  // If non-nil, direct this node's Stderr to os.Stderr
  RedirectStderr bool `json:"redirectStderr"`
}
```

As you can see, some fields of the config must be set, while others will be auto-generated if not provided. Bootstrap IPs/ IDs will be overwritten even if provided.

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

Th function `NewNetwork` returns a new network, parameterized on `network.Config`:

```go
type Config struct {
  // Must not be empty
  Genesis string `json:"genesis"`
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
}
```

The function that returns a new network may have additional configuration fields.

## Default Network Creation

The helper function `NewDefaultNetwork` returns a network using a pre-defined configuration. This allows users to create a new network without needing to define any configurations.

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
  reassignPortsIfUsed,
) (network.Network, error)
```

The associated pre-defined configuration is also available to users by calling `NewDefaultConfig` function.

## Network Snapshots

A given network state, including the node ports and the full blockchain state, can be saved to a named snapshot. The network can then be restarted from such a snapshot any time later.

```go
// Save network snapshot
// Network is stopped in order to do a safe persistence
// Returns the full local path to the snapshot dir
SaveSnapshot(context.Context, string) (string, error)
// Remove network snapshot
RemoveSnapshot(string) error
// Get names of all available snapshots
GetSnapshotNames() ([]string, error)
```

To create a new network from a snapshot, the function `NewNetworkFromSnapshot` is provided.

## Network Interaction

The network runner allows users to interact with an AvalancheGo network using the `network.Network` interface:

```go
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
  RemoveNode(name string) error
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
}
```

and allows users to interact with a node using the `node.Node` interface:

```go
// Node represents an AvalancheGo node
type Node interface {
  // Return this node's name, which is unique
  // across all the nodes in its network.
  GetName() string
  // Return this node's Avalanche node ID.
  GetNodeID() ids.ShortID
  // Return a client that can be used to make API calls.
  GetAPIClient() api.Client
  // Return this node's IP (e.g. 127.0.0.1).
  GetURL() string
  // Return this node's P2P (staking) port.
  GetP2PPort() uint16
  // Return this node's HTTP API port.
  GetAPIPort() uint16
  // Starts a new test peer, connects it to the given node, and returns the peer.
  // [handler] defines how the test peer handles messages it receives.
  // The test peer can be used to send messages to the node it's attached to.
  // It's left to the caller to maintain a reference to the returned peer.
  // The caller should call StartClose() on the peer when they're done with it.
  AttachPeer(ctx context.Context, handler router.InboundHandler) (peer.Peer, error)
  // Return this node's avalanchego binary path
  GetBinaryPath() string
  // Return this node's db dir
  GetDbDir() string
  // Return this node's logs dir
  GetLogsDir() string
  // Return this node's config file contents
  GetConfigFile() string
}
```

## FAQ

**Why does `avalanche-network-runner` need an RPC server?** `avalanche-network-runner` needs to provide complex workflows such as replacing nodes, restarting nodes, injecting fail points, etc.. The RPC server exposes basic operations to enable a separation of concerns such that one team develops a test framework, and the other writes test cases and controlling logic.

**Why gRPC?** The RPC server leads to more modular test components, and gRPC enables greater flexibility. The protocol buffer increases flexibility as we develop more complicated test cases. And gRPC opens up a variety of different approaches for how to write test controller (e.g., Rust). See [`rpcpb/rpc.proto`](./rpcpb/rpc.proto) for service definition.

**Why gRPC gateway?** [gRPC gateway](https://grpc-ecosystem.github.io/grpc-gateway/) exposes gRPC API via HTTP, without us writing any code. Which can be useful if a test controller writer does not want to deal with gRPC.
