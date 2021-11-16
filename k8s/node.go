package k8s

import (
	"github.com/ava-labs/avalanche-network-runner/api"
	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"
	"github.com/ava-labs/avalanchego/ids"
)

// ObjectSpec is the K8s-specifc object config. This is the "implementation-specic config"
// for the K8s-based network runner. See struct Config in network/node/node.go.
type ObjectSpec struct {
	Namespace  string `json:"namespace"`  // The kubernetes Namespace
	Identifier string `json:"identifier"` // Identifies this network in the cluster
	Kind       string `json:"kind"`       // Identifies the object Kind for the operator
	APIVersion string `json:"apiVersion"` // The APIVersion of the kubernetes object
	Image      string `json:"image"`      // The docker image to use
	Tag        string `json:"tag"`        // The docker tag to use
	Genesis    string `json:"genesis"`    // The genesis conf file for all nodes
}

// Node is a Avalanchego representation on k8s
// TODO rename this struct node?
type Node struct {
	// This node's AvalancheGo node ID
	nodeID ids.ShortID
	// Unique name of this node
	name string
	// URI of this node from Kubernetes
	uri string
	// Use to send API calls to this node
	client api.Client
	// K8s description of this node
	k8sObj *k8sapi.Avalanchego
}

// GetAPIClient returns the client to access the avalanchego API
func (n *Node) GetAPIClient() api.Client {
	return n.client
}

// GetName returns the string representation of this node
func (n *Node) GetName() string {
	return n.name
}

// GetNodeID returns the ShortID for this node
func (n *Node) GetNodeID() ids.ShortID {
	return n.nodeID
}

// GetURI returns the k8s URI to talk to the node
func (n *Node) GetURI() string {
	return n.uri
}

// GetK8sObject returns the kubernetes object representing a node in the kubernetes cluster
func (n *Node) GetK8sObject() *k8sapi.Avalanchego {
	return n.k8sObj
}
