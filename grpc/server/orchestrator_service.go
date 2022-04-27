// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
)

type OrchestratorServiceHandler struct {
	rpcpb.UnimplementedOrchestratorServiceServer

	orchestrator backend.NetworkOrchestrator
}

func NewOrchestatorServiceHandler(orchestrator backend.NetworkOrchestrator) *OrchestratorServiceHandler {
	return &OrchestratorServiceHandler{
		orchestrator: orchestrator,
	}
}

func (o *OrchestratorServiceHandler) CreateNetwork(ctx context.Context, req *rpcpb.CreateNetworkRequest) (*rpcpb.CreateNetworkResponse, error) {
	// Create the network, but do not save any information in the service handler.
	// The network is still accessible via the orchestrator by its unique name, which can be used
	// as the key to access it.
	_, err := o.orchestrator.CreateNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	return &rpcpb.CreateNetworkResponse{}, nil
}

func (o *OrchestratorServiceHandler) GetNodes(ctx context.Context, req *rpcpb.GetNodesRequest) (*rpcpb.GetNodesResponse, error) {
	network, err := o.orchestrator.GetNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	nodes, err := network.GetNodes()
	if err != nil {
		return nil, err
	}

	nodeInfos := make([]*rpcpb.NodeInfo, 0, len(nodes))
	for _, node := range nodes {
		configBytes, err := json.Marshal(node.Config())
		if err != nil {
			return nil, err
		}
		nodeInfos = append(nodeInfos, &rpcpb.NodeInfo{
			Name:        node.GetName(),
			Config:      configBytes,
			Uri:         node.GetHTTPBaseURI(),
			Bootstrapip: node.GetBootstrapIP(),
		})
	}

	return &rpcpb.GetNodesResponse{
		Nodes: nodeInfos,
	}, nil
}

func (o *OrchestratorServiceHandler) GetNode(ctx context.Context, req *rpcpb.GetNodeRequest) (*rpcpb.GetNodeResponse, error) {
	network, err := o.orchestrator.GetNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	node, err := network.GetNode(req.Name)
	if err != nil {
		return nil, err
	}

	configBytes, err := json.Marshal(node.Config())
	if err != nil {
		return nil, err
	}

	return &rpcpb.GetNodeResponse{
		Node: &rpcpb.NodeInfo{
			Name:        node.GetName(),
			Config:      configBytes,
			Uri:         node.GetHTTPBaseURI(),
			Bootstrapip: node.GetBootstrapIP(),
		},
	}, nil
}

func (o *OrchestratorServiceHandler) AddNode(ctx context.Context, req *rpcpb.AddNodeRequest) (*rpcpb.AddNodeResponse, error) {
	network, err := o.orchestrator.GetNetwork(req.Network)
	if err != nil {
		return nil, err
	}
	nodeConfig := backend.NodeConfig{}
	if err := json.Unmarshal(req.Config, &nodeConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal node config: %w", err)
	}
	node, err := network.AddNode(ctx, nodeConfig)
	if err != nil {
		return nil, err
	}
	configBytes, err := json.Marshal(node.Config())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal newly created node config: %w", err)
	}

	return &rpcpb.AddNodeResponse{
		Node: &rpcpb.NodeInfo{
			Name:        node.GetName(),
			Config:      configBytes,
			Uri:         node.GetHTTPBaseURI(),
			Bootstrapip: node.GetBootstrapIP(),
		},
	}, nil
}

func (o *OrchestratorServiceHandler) Teardown(ctx context.Context, req *rpcpb.TeardownRequest) (*rpcpb.TeardownResponse, error) {
	network, err := o.orchestrator.GetNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	return &rpcpb.TeardownResponse{}, network.Teardown(ctx)
}

func (o *OrchestratorServiceHandler) NodeStop(ctx context.Context, req *rpcpb.NodeStopRequest) (*rpcpb.NodeStopResponse, error) {
	network, err := o.orchestrator.GetNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	if err := network.RemoveNode(req.Name, time.Duration(req.Timeout)); err != nil {
		return nil, err
	}

	return &rpcpb.NodeStopResponse{}, nil
}
