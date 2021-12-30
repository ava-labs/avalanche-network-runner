package vms

import (
	"time"

	"github.com/ava-labs/avalanchego/api"
)

const (
	loopTimeout         = 1 * time.Second
	longTimeout         = 10 * loopTimeout
	defaultKeyThreshold = 1

	validatorWeight    = 3000
	validatorStartDiff = 30 * time.Second
	validatorEndDiff   = 30 * 24 * time.Hour // 30 days
)

var defaultUserPass = api.UserPass{Username: string("defaultUser"), Password: "0L1cuAnq2q14z51WbvWfu3kS"}
