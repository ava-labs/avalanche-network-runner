package constants

import "time"

const (
	HealthCheckFreq = 5 * time.Second
	HealthyTimeout  = 100 * time.Second
	TimeoutDuration = 60 * time.Second
	DefaultPort     = 9650
)
