package localnetworkrunner

import (
	"github.com/ava-labs/avalanche-testing/avalanche/networkrunner"
	"github.com/ava-labs/avalanchego/ids"
)

type Node struct {
	id     ids.ID
	client APIClient
}

func (node *Node) GetID() ids.ID {
	return node.id
}

func (node *Node) GetAPIClient() networkrunner.APIClient {
	return node.client
}
