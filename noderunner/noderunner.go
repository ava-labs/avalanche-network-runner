package noderunner

import "github.com/ava-labs/avalanche-testing/avalanche/libs/avalanchegoclient"

// NodeRunner encapsulates data for a running node in the test environment
type NodeRunner struct {
	client    *Client
	ipAddress string
	nodeID    string
	name      string
	httpPort  uint
}

func NewNodeRunner(name string, nodeID string, ipAddress string, httpPort uint, client *Client) (*NodeRunner, error) {
	return &NodeRunner{
		name:      name,
		nodeID:    nodeID,
		client:    client,
		ipAddress: ipAddress,
		httpPort:  httpPort,
	}, nil
}

// GetNodeID returns the node ID of this node
func (r *NodeRunner) GetNodeID() string {
	return r.nodeID
}

// GetName returns the name of this node as string
func (r *NodeRunner) GetName() string {
	return r.name
}

// GetIPAddress returns the IP address of this node
func (r *NodeRunner) GetIPAddress() string {
	return r.ipAddress
}

// GetHTTPPort returns the port this node runs on
func (r *NodeRunner) GetHTTPPort() uint {
	return r.httpPort
}

// GetClient returns the avalanchego client which allows to make calls to the node
func (r *NodeRunner) GetClient() *Client {
	return r.client
}
