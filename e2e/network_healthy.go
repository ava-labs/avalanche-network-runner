// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanchego/api/health"
	"golang.org/x/sync/errgroup"
)

// AwaitHealthy returns a nil error after all of the nodes in [network] report healthy.
// Forwards the parameters [parentCtx] and [healthCheckFreq] into AwaitHealthy
func AwaitHealthy(parentCtx context.Context, network backend.Network, healthCheckFreq time.Duration) error {
	eg, ctx := errgroup.WithContext(parentCtx)
	nodes, err := network.GetNodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		node := node
		client := health.NewClient(node.GetHTTPBaseURI())
		eg.Go(func() error {
			healthy, err := client.AwaitHealthy(ctx, healthCheckFreq)
			if err != nil {
				return err
			}
			if !healthy {
				return fmt.Errorf("node %s never became healthy", node.GetName())
			}
			return nil
		})
	}

	return eg.Wait()
}
