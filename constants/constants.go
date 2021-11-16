package constants

import "time"

const (
	HealthCheckFreq    = 5 * time.Second
	HealthyTimeout     = 100 * time.Second
	APITimeoutDuration = 10 * time.Second
	DefaultPort        = 9650
	DefaultNetworkID   = uint32(1337)
)
