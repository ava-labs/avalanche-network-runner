// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/networks"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestNetworkOrchestrator tests that [orchestator] can be used to construct the default local network and wait for all of the clients
// to get healthy before tearing the network down.
func TestNetworkOrchestrator(ctx context.Context, t *testing.T, orchestrator backend.NetworkOrchestrator) {
	assert := assert.New(t)

	defer func() {
		closeCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Minute))
		defer cancel()
		assert.NoError(orchestrator.Teardown(closeCtx))
	}()

	logrus.Info("Creating default local network")
	network, err := networks.NewDefaultLocalNetwork(ctx, orchestrator, constants.NormalExecution)
	if err != nil {
		t.Fatal(err)
	}

	assert.NoError(AwaitHealthy(ctx, network, 5*time.Second))
}
