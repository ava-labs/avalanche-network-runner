# avalanche-network-runner-local

Tool to run and interact with an Avalanche network locally

## Overview

Golang Library to interact with an Avalanche network from a user client software.

Network creation:

```
func NewNetwork(logging.Logger, network.Config, map[NodeType]string)
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

Clone https://github.com/ava-labs/avalanche-network-runner-local to GOPATH (into $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local)

GO111MODULE=off go get github.com/ava-labs/avalanche-network-runner-local

cd $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local

go run examples/local/main.go

(Assumes you have the AvalancheGo v1.6.4 binaries at $GOPATH/src/github.com/ava-labs/avalanchego/build)

This starts a local network with 5 nodes, waits until the nodes are healthy, prints their names, then stops all the nodes.

