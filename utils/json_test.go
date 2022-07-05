// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetJSONKey(t *testing.T) {
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
		"whitelisted-subnets":"a,b,c",
		"plugin-dir":"INFO"
	}`
	s, err := SetJSONKey(b, "whitelisted-subnets", "d,e,f")
	assert.NoError(t, err)
	assert.Contains(t, s, `"whitelisted-subnets":"d,e,f"`)
	// now check it's actual correct JSON
	var m map[string]interface{}
	err = json.Unmarshal([]byte(s), &m)
	assert.NoError(t, err)
	// check if one-liner also works
	bb := `{"api-admin-enabled":true,"api-ipcs-enabled":true,"db-dir":"/tmp/network-runner-root-data3856302950/node5/db-dir","health-check-frequency":"2s","index-enabled":true,"log-dir":"/tmp/network-runner-root-data3856302950/node5/log","log-display-level":"INFO","log-level":"INFO","network-max-reconnect-delay":"1s","network-peer-list-gossip-frequency":"250ms","plugin-dir":"/home/fabio/go/src/github.com/ava-labs/avalanchego/build/plugins","public-ip":"127.0.0.1","whitelisted-subnets":""}`
	ss, err := SetJSONKey(bb, "whitelisted-subnets", "d,e,f")
	assert.NoError(t, err)
	assert.Contains(t, s, `"whitelisted-subnets":"d,e,f"`)
	// also check here it's correct JSON
	err = json.Unmarshal([]byte(ss), &m)
	assert.NoError(t, err)
}
