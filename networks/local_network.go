// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package networks

import (
	"context"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/config"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const defaultLocalNetworkName = "defaultLocalNetwork"

// NewDefaultLocalNetwork uses orchestrator to generate a new network that runs with 5 nodes on the default local network
func NewDefaultLocalNetwork(ctx context.Context, orchestrator backend.NetworkOrchestrator, executable string) (backend.Network, error) {
	network, err := orchestrator.CreateNetwork(fmt.Sprintf("%s-%v", defaultLocalNetworkName, time.Now().Unix()))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if err := network.Teardown(ctx); err != nil {
				logrus.Errorf("Failed to tear down network after failing to start local network: %s", err)
			}
		}
	}()

	networkConfig := CreateLocalNetworkConfig(executable)
	if len(networkConfig.Nodes) != len(constants.LocalNetworkStakerIDs) {
		return nil, fmt.Errorf("unexpected number of nodes in local network config: %d", len(networkConfig.Nodes))
	}

	bootNode, err := network.AddNode(ctx, networkConfig.Nodes[0])
	if err != nil {
		return nil, fmt.Errorf("failed to add node %s: %w", networkConfig.Nodes[0].Name, err)
	}

	bootstrapIP := bootNode.GetBootstrapIP()
	eg := errgroup.Group{}
	for i := 1; i < 5; i++ {
		i := i
		eg.Go(func() error {
			nodeConfig := networkConfig.Nodes[i]
			// Must override [bootstrap-ips] since we cannot know this before this point.
			nodeConfig.Config[config.BootstrapIPsKey] = bootstrapIP

			_, err := network.AddNode(ctx, nodeConfig)
			if err != nil {
				return err
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		if teardownErr := network.Teardown(ctx); err != nil {
			logrus.Errorf("failed to teardown network due to %s after wait group errored with %s", teardownErr, err)
		}
		return nil, err
	}
	return network, nil
}
