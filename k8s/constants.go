package k8s

import "time"

// TODO make these configurable
const (
	resourceLimitsCPU      = "1"
	resourceLimitsMemory   = "4Gi"
	resourceRequestCPU     = "500m"
	resourceRequestMemory  = "2Gi"
	stopTimeout            = 10 * time.Second
	removeTimeout          = 30 * time.Second
	nodeReachableCheckFreq = 5 * time.Second
	healthCheckFreq        = 3 * time.Second
	// TODO export these default ports from the
	// AvalancheGo operator and use the imported
	// values instead of re-defining them below.
	defaultAPIPort = uint16(9650)
	defaultP2PPort = uint16(9651)
)
