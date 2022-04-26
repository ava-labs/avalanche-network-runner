// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"context"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/e2e"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
)

func TestLocalNetworkOrchestrator(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()

	registry := backend.NewExecutorRegistry(map[string]string{
		constants.NormalExecution: constants.AvalancheGoBinary,
	})

	// Note: t.TempDir() returns a directory that can be cleaned up within the test, whereas
	// os.TempDir() maintains an open file descriptor, so it cannot be cleaned up by the
	// network orchestrator.
	orchestrator := NewNetworkOrchestrator(t.TempDir(), registry, true)

	e2e.TestNetworkOrchestrator(ctx, t, orchestrator)
}
