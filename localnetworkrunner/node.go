package localnetworkrunner

import (
	"errors"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner-local/networkrunner"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
)

type Node struct {
	id     ids.ID
	nodeID *ids.ShortID
	client APIClient
}

func (node Node) GetID() ids.ID {
	return node.id
}

func (node Node) GetNodeID() (ids.ShortID, error) {
	if node.nodeID == nil {
		info := node.client.GetNodeRunner().GetClient().InfoAPI()
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

func (node Node) GetAPIClient() networkrunner.APIClient {
	return node.client
}
