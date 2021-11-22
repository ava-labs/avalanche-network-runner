package constants

import "time"

const (
	HealthCheckInterval = 3 * time.Second
	APITimeoutDuration  = 10 * time.Second
	DefaultAPIPort      = 9650
	DefaultStakingPort  = 9651
)
