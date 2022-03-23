package k8s

import (
	"context"
	"errors"
	"net"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/snow/networking/router"
)

var _ node.Node = &Node{}

// ObjectSpec is the K8s-specifc object config. This is the "implementation-specic config"
// for the K8s-based network runner. See struct Config in network/node/node.go.
type ObjectSpec struct {
	Namespace  string `json:"namespace"`  // The kubernetes Namespace
	Identifier string `json:"identifier"` // Identifies this network in the cluster
	Kind       string `json:"kind"`       // Identifies the object Kind for the operator
	APIVersion string `json:"apiVersion"` // The APIVersion of the kubernetes object
	Image      string `json:"image"`      // The docker image to use
	Tag        string `json:"tag"`        // The docker tag to use
}

// Node is a Avalanchego representation on k8s
type Node struct {
	// This node's AvalancheGo node ID
	nodeID ids.ShortID
	// Unique name of this node
	name string
	// URI of this node from Kubernetes
	uri string
	// Use to send API calls to this node
	apiClient api.Client
	// K8s description of this node
	k8sObjSpec *k8sapi.Avalanchego
}

// AttachPeer see Network
// TODO: Not yet implemented
func (n *Node) AttachPeer(ctx context.Context, handler router.InboundHandler) (peer.Peer, error) {
	return nil, errors.New("AttachPeer is not implemented")
}

// See node.Node
func (n *Node) GetAPIClient() api.Client {
	return n.apiClient
}

// See node.Node
func (n *Node) GetName() string {
	return n.name
}

// See node.Node
func (n *Node) GetNodeID() ids.ShortID {
	return n.nodeID
}

// See node.Node
func (n *Node) GetURL() string {
	return n.uri
}

// See node.Node
func (n *Node) GetP2PPort() uint16 {
	// TODO don't hard-code this
	return defaultP2PPort
}

func (n *Node) GetAPIPort() uint16 {
	// TODO don't hard-code this
	return defaultAPIPort
}

// GetK8sObjSpec returns the kubernetes object spec
// representation of this node
func (n *Node) GetK8sObjSpec() *k8sapi.Avalanchego {
	return n.k8sObjSpec
}

func (n *Node) GetConnFunc() node.GetConnFunc {
	return func(context.Context, node.Node) (net.Conn, error) {
		return nil, errors.New("unimplemented")
	}
}
