package vms

import (
	"time"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/utils/units"
)

const (
	apiRetryFreq        = 1 * time.Second
	bootstrapTimeout    = time.Minute
	defaultKeyThreshold = 1
	validatorWeight     = 3000 * units.Avax
	validatorStartDiff  = 30 * time.Second
	validatorEndDiff    = 30 * 24 * time.Hour // 30 days
)

var defaultUserPass = api.UserPass{Username: string("defaultUser"), Password: "0L1cuAnq2q14z51WbvWfu3kS"}
