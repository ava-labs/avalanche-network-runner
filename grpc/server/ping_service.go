// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"os"

	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"go.uber.org/zap"
)

type PingServiceHandler struct {
	rpcpb.UnimplementedPingServiceServer
}

func (h *PingServiceHandler) Ping(ctx context.Context, in *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	zap.L().Debug("received ping request")
	return &rpcpb.PingResponse{Pid: int32(os.Getpid())}, nil
}
