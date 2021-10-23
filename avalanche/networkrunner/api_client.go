package networkrunner

import (
	oldrunner "github.com/ava-labs/avalanche-testing/avalanche/builder/networkrunner"
)

// Issues API calls to a node
type APIClient interface {
	// TODO add methods
	GetNodeRunner() *oldrunner.NodeRunner
}
