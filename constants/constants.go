package constants

import "time"

const (
	HealthCheckFreq = 5 * time.Second
	HealthyTimeout  = 100 * time.Second
	TimeoutDuration = 60 * time.Second
	DefaultPort     = 9650

	K8sResourceLimitsCPU    = "1"
	K8sResourceLimitsMemory = "4Gi"

	K8sResourceRequestCPU    = "500m"
	K8sResourceRequestMemory = "2Gi"
)
