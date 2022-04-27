// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
)

var _ backend.Node = &node{}

type node struct {
	network  string
	nodeInfo *rpcpb.NodeInfo
	config   map[string]interface{}
	client   rpcpb.OrchestratorServiceClient
}

func newNode(network string, nodeInfo *rpcpb.NodeInfo, client rpcpb.OrchestratorServiceClient) (backend.Node, error) {
	config := make(map[string]interface{})
	if err := json.Unmarshal(nodeInfo.Config, &config); err != nil {
		return nil, err
	}

	return &node{
		network:  network,
		nodeInfo: nodeInfo,
		config:   config,
		client:   client,
	}, nil
}

func (n *node) GetName() string { return n.nodeInfo.Name }

func (n *node) Config() map[string]interface{} { return n.config }

func (n *node) GetHTTPBaseURI() string { return n.nodeInfo.Uri }

func (n *node) GetBootstrapIP() string { return n.nodeInfo.Bootstrapip }

func (n *node) Stop(timeout time.Duration) error {
	_, err := n.client.NodeStop(context.Background(), &rpcpb.NodeStopRequest{
		Network: n.network,
		Name:    n.nodeInfo.Name,
		Timeout: int64(timeout),
	})
	return err
}
