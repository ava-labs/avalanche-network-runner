package localnetworkrunner

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
)

// Gives access to basic nodes info, and to most avalanchego apis
type Node struct {
	id     ids.ID
	nodeID *ids.ShortID
	client APIClient
}

// interface compliance
var _ networkrunner.Node = (*Node)(nil)

// Returs internal id used by the network runner
// Incremental from 1..
// 1:1 mapping to network config node positions
func (node Node) GetID() ids.ID {
	return node.id
}

// Returns avalanchego node id as obtained from InfoAPI
func (node Node) GetNodeID() (ids.ShortID, error) {
	if node.nodeID == nil {
		info := node.client.InfoAPI()
		strNodeID, err := info.GetNodeID()
		if err != nil {
			return ids.ShortID{}, errors.New(fmt.Sprintf("could not obtain id from info api: %s", err))
		}
		nodeID, err := ids.ShortFromPrefixedString(strNodeID, constants.NodeIDPrefix)
		if err != nil {
			return ids.ShortID{}, errors.New(fmt.Sprintf("could not convert node id from string: %s", err))
		}
		node.nodeID = &nodeID
	}
	return *node.nodeID, nil
}

// Returns access to avalanchego apis
func (node Node) GetAPIClient() networkrunner.APIClient {
	return node.client
}
