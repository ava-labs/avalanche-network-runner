package utils

import (
	"context"

	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/api/info"
)

func GetNetworkIDFromEndpoint(endpoint string) (uint32, error) {
	infoClient := info.NewClient(endpoint)
	ctx, cancel := context.WithTimeout(context.Background(), constants.HttpRequestTimeout)
	defer cancel()
	return infoClient.GetNetworkID(ctx)
}
