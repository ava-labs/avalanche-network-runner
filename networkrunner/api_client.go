package networkrunner

import (
	"github.com/ava-labs/avalanche-network-runner-local/noderunner"
)

// Issues API calls to a node
type APIClient interface {
	// TODO add methods
	GetNodeRunner() *noderunner.NodeRunner
}
