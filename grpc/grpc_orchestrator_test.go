// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/e2e"
	"github.com/ava-labs/avalanche-network-runner/grpc/client"
	"github.com/ava-labs/avalanche-network-runner/grpc/server"
	"github.com/ava-labs/avalanche-network-runner/localbinary"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestOrchestratorGRPC(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()

	// Note: t.TempDir() returns a directory that can be cleaned up within the test, whereas
	// os.TempDir() maintains an open file descriptor, so it cannot be cleaned up by the
	// network orchestrator.
	orchestrator := localbinary.NewNetworkOrchestrator(&localbinary.OrchestratorConfig{
		BaseDir: t.TempDir(),
		Registry: map[string]string{
			constants.NormalExecution: constants.AvalancheGoBinary,
		},
		DestroyOnTeardown: true,
	})
	// We simply tear down the underlying network constructor instead of tearing down the created client, since the client
	// does not support the teardown operation.
	defer func() {
		assert.NoError(t, orchestrator.Teardown(ctx))
	}()

	// Spin up the client and server, so that we can test the client network orchestrator implementation.
	server, err := server.New(server.Config{
		Port:        ":8080",
		GwPort:      ":8081",
		DialTimeout: 10 * time.Second,
	}, orchestrator)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		assert.NoError(t, server.Run(ctx), "server run error")
	}()

	client, err := client.New(client.Config{
		LogLevel:    zapcore.InfoLevel.String(),
		Endpoint:    "localhost:8080",
		DialTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		assert.NoError(t, client.Close(), "closing grpc client")
	}()

	e2e.TestNetworkOrchestrator(ctx, t, client)
}
