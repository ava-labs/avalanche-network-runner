package k8s

import (
	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanchego/ids"
)

// K8sNode is a Avalanchego representation on k8s
type K8sNode struct {
	nodeID ids.ShortID
	name   string
	uri    string
	client api.Client
}

// GetAPIClient returns the client to access the avalanchego API
func (n *K8sNode) GetAPIClient() api.Client {
	return n.client
}

// GetName returns the string representation of this node
func (n *K8sNode) GetName() string {
	return n.name
}

// GetNodeID returns the ShortID for this node
func (n *K8sNode) GetNodeID() ids.ShortID {
	return n.nodeID
}

// GetURI returns the k8s URI to talk to the node
func (n *K8sNode) GetURI() string {
	return n.uri
}

// Return API access url
func (n *K8sNode) GetAPIURL() string {
	return ""
}

// Return staking url
func (n *K8sNode) GetStakingURL() string {
	return ""
}
