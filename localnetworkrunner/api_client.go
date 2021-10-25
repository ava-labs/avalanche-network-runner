package localnetworkrunner

import (
	"github.com/ava-labs/avalanche-network-runner-local/noderunner"
)

type APIClient struct {
	runner *noderunner.NodeRunner
}

var _ noderunner.APIClient = (*APIClient)(nil)

func (apiClient APIClient) GetNodeRunner() *noderunner.NodeRunner {
	return apiClient.runner
}
