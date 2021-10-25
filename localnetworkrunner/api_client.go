package localnetworkrunner

import (
	"github.com/ava-labs/avalanche-network-runner-local/noderunner"
)

type APIClient struct {
	runner *noderunner.NodeRunner
}

var _ networkrunner.APIClient = (*APIClient)(nil)

func (apiClient APIClient) GetNodeRunner() *noderunner.NodeRunner {
	return apiClient.runner
}
