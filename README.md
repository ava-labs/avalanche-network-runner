# avalanche-network-runner-local

Tool to run and interact with an Avalanche network locally

## Overview

Golang Library to interact with an Avalanche network from a user client software. The network
is local to the use machine, that is, it consists of different OS processes for different nodes.

### Network creation

A network can be created from a specification of the starting nodes on it, including
the special case of cero nodes. Then subsequent nodes can then be added. 

```
func NewNetwork(log logging.Logger, networkConfig network.Config, nodeTypeToBinaryPath map[NodeType]string)
```

It takes a `network.Config` as defined in `github.com/ava-labs/avalanche-network-runner-local/network`, which
is a list of `node.Config` as defned in `github.com/ava-labs/avalanche-network-runner-local/network/node` and
`github.com/ava-labs/avalanche-network-runner-local/local`.

Node fields common to all backends (local,k8s):

- Name: reference name for the nodes. if not given, it is generated.
- IsBeacon: indicator that the node is going to be a bootstrapper for the other nodes in the network. at least one node should be bootstrapper.
- StakingKey: key file contents for the node. if not given (and also cert should not be given), it is generated.
- StakingCert: cert file contents for the node. if not given (and also key should not be given), it is generated.
- CChainConfigFile: c chain config file for the node. optional.
- ConfigFile: config file for the node. should be defined.
- GenesisFile: genesis file for the node. should be defined.

Node fields specific to local backend (ImplSpecificConfig)
- Type: kind of node, one of the values on NodeType (currently AVALANCHEGO, BYZANTINE). a map is given on network creation (nodeTypeToBinaryPath), that specifies the node binary to execute, depending on the contents of this field.
- Stdout: optional. writer to which to associate the standard output of the node.
- Stderr: optional. writer to which to associate the standard error of the node.

### Network manipulation

```
func (*localNetwork) AddNode(nodeConfig node.Config) (node.Node, error) 
func (*localNetwork) RemoveNode(nodeName string) error 
func (*localNetwork) Healthy() chan error 
func (*localNetwork) GetNode(nodeName string) (node.Node, error) 
func (*localNetwork) GetNodesNames() ([]string, error) 
func (*localNetwork) Stop(ctx context.Context) error 
```

## Demo example

The demo starts a local network with 5 nodes, waits until the nodes are healthy, prints their names, then stops all the nodes.

It assumes you have the AvalancheGo v1.6.4 binaries at `$GOPATH/src/github.com/ava-labs/avalanchego/build`

Clone:

`https://github.com/ava-labs/avalanche-network-runner-local` to `GOPATH` (expected location: `$GOPATH/src/github.com/ava-labs/avalanche-network-runner-local`)

```
GO111MODULE=off go get github.com/ava-labs/avalanche-network-runner-local
```

Execute example:

```
cd $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local
go run examples/local/main.go
```

