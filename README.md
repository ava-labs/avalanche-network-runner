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

### Examples and references

[Examples of different network control commands.](/docs/examples.md)

[Complete examples of blockchain creation.](/docs/blockchain-examples.md)

[Commands Reference.](/docs/reference.md)

[Using ANR core golang lib.](/docs/lib.md)

### RPC FAQ

**Why does `avalanche-network-runner` need an RPC server?** `avalanche-network-runner` needs to provide complex workflows such as replacing nodes, restarting nodes, injecting fail points, etc.. The RPC server exposes basic operations to enable a separation of concerns such that one team develops a test framework, and the other writes test cases and controlling logic.

**Why gRPC?** The RPC server leads to more modular test components, and gRPC enables greater flexibility. The protocol buffer increases flexibility as we develop more complicated test cases. And gRPC opens up a variety of different approaches for how to write test controller (e.g., Rust). See [`rpcpb/rpc.proto`](./rpcpb/rpc.proto) for service definition.

**Why gRPC gateway?** [gRPC gateway](https://grpc-ecosystem.github.io/grpc-gateway/) exposes gRPC API via HTTP, without us writing any code. Which can be useful if a test controller writer does not want to deal with gRPC.
