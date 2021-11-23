package testconstants

import "time"

const (
	DefaultNetworkID   = uint32(1337)
	DefaultNetworkSize = 5
	StopTimeout        = 10 * time.Second
	RemoveTimeout      = 30 * time.Second
	PollTimeout        = 5 * time.Second
)
