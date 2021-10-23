package localnetworkrunner

import (
    "github.com/ava-labs/avalanche-testing/avalanche/networkrunner"
)

type Node struct {
    client APIClient
}

func (node *Node) GetAPIClient() networkrunner.APIClient {
    return node.client
}
