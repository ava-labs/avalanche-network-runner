# avalanche-network-runner

Tool to run and interact with an Avalanche network, deployed either in localhost or in a kubernetes cluster.

## Overview

Golang Library to interact with an Avalanche network from a user client software, with development or testing purposes. 

The network can be:

1. local to the use machine, that is, consisting of different operating system processes, instantiated from avalanchego executables, assigned to different avalanche nodes.
2. deployed in a kubernetes cluster, consisting of different kubernetes pods, instantiated from avalanchego containers, assigned to different avalanche nodes.

All library functions are race free, so can be safely executed from concurrent user software.

The configuration for each node is by default automatically generated, including:
- api port of the node
- staking port of the node
- reference to bootstrap nodes (by using user specified IsBeacon field in network config)
- cert/key contents (if not given)
- paths and files for genesis, config, cchain conf, cert/key, from given or generated []byte contents
- log/db directories

Besides that, parts of the configuration for the node can be specified by the user, for example:
- genesis file
- config file
- cchain config file
- cert/key file

### Network creation

A network can be created from a specification of the starting nodes on it, including he special case of cero nodes. In any case, subsequently new nodes can be added, and nodes can be removed.

```
func NewNetwork(log logging.Logger, networkConfig network.Config, ...)
```

It takes a `network.Config` (basically a list of `node.Config` items), an returns an implementation (either local or k8s) of the interface `network.Network`. 

Where:
- `network.Network` is defined in `github.com/ava-labs/avalanche-network-runner-local/network/network.go`
- `network.Config` is defined in `github.com/ava-labs/avalanche-network-runner-local/network/network.go`
- `node.Config` is defined in `github.com/ava-labs/avalanche-network-runner-local/network/node/node.go`

Node fields common to all backends (local,k8s):

- **Name**: optional. unique reference name for the node. if not given, it is generated
- **IsBeacon**: optional. indicator that the node is going to be a bootstrapper for the other nodes in the network. at least one node should be bootstrapper
- **StakingKey**: optional. key file contents for the node. if not given , it is generated (note also cert should not be given)
- **StakingCert**: optional. cert file contents for the node. if not given, it is generated (note also key should not be given)
- **CChainConfigFile**: optional. c chain config file for the node
- **ConfigFile**: config file for the node
- **GenesisFile**: genesis file for the node

Node fields specific to local backend (`ImplSpecificConfig`):
- **BinaryPath**: specifies the node binary to execute
- **Stdout**: optional. writer to which to associate the standard output of the node.
- **Stderr**: optional. writer to which to associate the standard error of the node.

Node fields specific to k8s backend (`ImplSpecificConfig`):

### Network manipulation

Functions used to change the nodes in the network, get information from the them, wait the network to be ready, and stopping it.

```
func (*localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) 
func (*localNetwork) RemoveNode(nodeName string) error 
func (*localNetwork) Healthy() chan error 
func (*localNetwork) GetNode(nodeName string) (node.Node, error) 
func (*localNetwork) GetNodesNames() ([]string, error) 
func (*localNetwork) Stop(ctx context.Context) error 
```

### Node interaction

A given node, obtained from `GetNode()` can be queried for its name and avalanchego id. 

```
GetName() string
GetNodeID() ids.ShortID
GetAPIClient() api.Client
```

`GetAPIClient()` give access to all subjacent avalanchego apis:

```
PChainAPI() api.PChainClient
XChainAPI() pi.AvmClient
XChainWalletAPI() api.AvmWalletClient
CChainAPI() api.EvmClient
CChainEthAPI() api.EthClient 
InfoAPI() api.InfoClient
HealthAPI() api.HealthClient
IpcsAPI() api.IpcsClient
KeystoreAPI() api.KeystoreClient
AdminAPI() api.AdminClient
PChainIndexAPI() api.IndexerClient
CChainIndexAPI() api.IndexerClient
```

Where most of then are interfaces over associated avalanchego or coreth apis, except `api.EthClient`, which is a wrapper over coreth `ethclient.Client` websocket, that add mutexed calls, and lazy start of connection (on first call).

## Instalation

Clone repository `https://github.com/ava-labs/avalanche-network-runner-local` to `$GOPATH`

For example by:

```
GO111MODULE=off go get github.com/ava-labs/avalanche-network-runner-local
```

## Unit testing

To verify status of the library, execute:

```
cd $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local
go test ./...
```

## Local network runner demo example 


The demo starts a local network with 5 nodes, waits until the nodes are healthy, prints their names, then stops all the nodes.

It assumes:

1. you have the AvalancheGo v1.6.4 binaries at `$GOPATH/src/github.com/ava-labs/avalanchego/build`
2. the library is located at `$GOPATH/src/github.com/ava-labs/avalanche-network-runner-local`)

Execution:

```
cd $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local
go run examples/local/main.go
```



