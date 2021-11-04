# avalanche-network-runner-local

Tool to run and interact with an Avalanche network locally

## Overview

Golang Library to interact with an Avalanche network from a user client software.

Network creation:

```
func NewNetwork(log logging.Logger, networkConfig network.Config, nodeTypeToBinaryPath map[NodeType]string)
```

Network manipulation:

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

