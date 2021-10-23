package localnetworkrunner

import (
	oldnetworkrunner "github.com/ava-labs/avalanche-testing/avalanche/builder/networkrunner"
)

type APIClient struct {
    runner *oldnetworkrunner.NodeRunner
}

func (apiClient APIClient) GetNodeRunner() *oldnetworkrunner.NodeRunner {
    return apiClient.runner
}
