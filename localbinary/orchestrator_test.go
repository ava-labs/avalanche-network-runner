// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"context"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/e2e"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/stretchr/testify/assert"
)

func TestLocalNetworkOrchestrator(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
	defer cancel()

	// Note: t.TempDir() returns a directory that can be cleaned up within the test, whereas
	// os.TempDir() maintains an open file descriptor, so it cannot be cleaned up by the
	// network orchestrator.
	orchestrator := NewNetworkOrchestrator(&OrchestratorConfig{
		BaseDir: t.TempDir(),
		Registry: map[string]string{
			constants.NormalExecution: constants.AvalancheGoBinary,
		},
		DestroyOnTeardown: true,
	})
	defer func() {
		assert.NoError(t, orchestrator.Teardown(ctx), "failed to teardown orchestator")
	}()

	e2e.TestNetworkOrchestrator(ctx, t, orchestrator)
}
