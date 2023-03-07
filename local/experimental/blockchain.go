package experimental

import (
	"context"

	"github.com/ava-labs/avalanche-network-runner/network"
)

func CreateSpecificBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) (map[string][]string, error) {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	chainInfos, err := ln.installCustomChains(ctx, chainSpecs)
	if err != nil {
		return nil, err
	}

	if err := ln.waitForCustomChainsReady(ctx, chainInfos); err != nil {
		return nil, err
	}

	if err := ln.RegisterBlockchainAliases(ctx, chainInfos, chainSpecs); err != nil {
		return nil, err
	}

	return nil, nil
}
