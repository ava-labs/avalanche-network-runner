package k8s

const (
	resourceLimitsCPU     = "1"
	resourceLimitsMemory  = "4Gi"
	resourceRequestCPU    = "500m"
	resourceRequestMemory = "2Gi"
	// TODO export these default ports from the
	// AvalancheGo operator and use the imported
	// values instead of re-defining them below.
	defaultAPIPort     = 9650
	defaultStakingPort = 9651
)
