// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package client

import (
	"context"
	"errors"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
)

var _ backend.OrchestratorBackend = &orchestrator{}

type orchestrator struct {
	client rpcpb.OrchestratorServiceClient
}

func newOrchestrator(client rpcpb.OrchestratorServiceClient) backend.OrchestratorBackend {
	return &orchestrator{
		client: client,
	}
}

func (o *orchestrator) CreateNetworkConstructor(name string) (backend.NetworkConstructor, error) {
	_, err := o.client.CreateNetwork(context.Background(), &rpcpb.CreateNetworkRequest{Network: name})
	if err != nil {
		return nil, err
	}
	return newNetwork(name, o.client), nil
}

func (o *orchestrator) Teardown(ctx context.Context) error {
	return errors.New("cannot tear down server network constructor")
}
