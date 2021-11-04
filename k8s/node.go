package k8s

import (
	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanchego/ids"
)

type K8sNode struct {
	ShortID ids.ShortID
	NodeID  string
	Client  api.Client
	URI     string
}

func (n *K8sNode) GetAPIClient() api.Client {
	return n.Client
}

func (n *K8sNode) GetName() string {
	return n.NodeID
}

func (n *K8sNode) GetNodeID() ids.ShortID {
	return n.ShortID
}

func (n *K8sNode) GetID() string {
	return n.NodeID
}
