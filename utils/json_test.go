// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpdateJSONKey(t *testing.T) {
	b := `{
		"network-peer-list-gossip-frequency":"250ms",
		"network-max-reconnect-delay":"1s",
		"public-ip":"127.0.0.1",
		"health-check-frequency":"2s",
		"api-admin-enabled":true,
		"api-ipcs-enabled":true,
		"index-enabled":true,
		"log-display-level":"INFO",
		"log-level":"INFO",
		"log-dir":"INFO",
		"db-dir":"INFO",
		"plugin-dir":"INFO",
		"whitelisted-subnets":"a,b,c"
	}`
	s, err := UpdateJSONKey(b, "whitelisted-subnets", "d,e,f")
	assert.NoError(t, err)
	assert.Contains(t, s, `"whitelisted-subnets":"d,e,f"`)
}
