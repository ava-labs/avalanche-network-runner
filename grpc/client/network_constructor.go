// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
)

var _ backend.NetworkConstructor = &networkConstructor{}

type networkConstructor struct {
	network string
	client  rpcpb.OrchestratorServiceClient
}

func newNetwork(network string, client rpcpb.OrchestratorServiceClient) backend.NetworkConstructor {
	return &networkConstructor{
		network: network,
		client:  client,
	}
}

func (n *networkConstructor) AddNode(ctx context.Context, config backend.NodeConfig) (backend.Node, error) {
	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal node config: %w", err)
	}
	res, err := n.client.AddNode(ctx, &rpcpb.AddNodeRequest{
		Network: n.network,
		Config:  configBytes,
	})
	if err != nil {
		return nil, err
	}

	return newNode(n.network, res.Node, n.client)
}

func (n *networkConstructor) Teardown(ctx context.Context) error {
	_, err := n.client.Teardown(ctx, &rpcpb.TeardownRequest{
		Network: n.network,
	})
	return err
}
