# avalanche-network-runner-local

Tool to run and interact with an Avalanche network locally

## Overview

Golang Library to interact with an Avalanche network from a user client software.

## Demo example

Clone https://github.com/ava-labs/avalanche-network-runner-local to GOPATH (into $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local)

GO111MODULE=off go get github.com/ava-labs/avalanche-network-runner-local

cd $GOPATH/src/github.com/ava-labs/avalanche-network-runner-local

go run examples/local/main.go

(Assumes you have the AvalancheGo v1.6.4 binaries at $GOPATH/src/github.com/ava-labs/avalanchego/build)

This starts a local network with 5 nodes, waits until the nodes are healthy, prints their names, then stops all the nodes.

