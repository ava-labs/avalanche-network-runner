package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalConfig(t *testing.T) {
	assert := assert.New(t)

	// test using the default config...
	test1 := defaultNodeConfig
	var test1Map map[string]interface{}
	err := json.Unmarshal([]byte(test1), &test1Map)
	assert.NoError(err)

	tLogLevel := "TRACE"
	tLogDir := "/tmp/log"
	tDbDir := "/tmp/db"
	tWhitelistedSubnets := "someSubnet"
	tPluginDir := "/tmp/plugins"

	finalJSON, err := createConfigFileString(test1, tLogLevel, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	var controlMap map[string]interface{}
	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	// ...all default config entries should still be there...
	for k, v := range test1Map {
		assert.Equal(controlMap[k], v)
	}
	// ...as well as additional ones
	assert.Equal(controlMap["log-level"], tLogLevel)
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)

	// now test a custom provided config
	test2 := `{
		"log-dir":"/home/user/logs",
		"db-dir":"/home/user/db",
		"plugin-dir":"/home/user/plugins",
		"log-display-level":"debug",
		"log-level":"debug",
		"whitelisted-subnets":"otherSubNets",
		"index-enabled":false,
		"public-ip":"192.168.0.1",
		"network-id":999,
		"tx-fee":9999999
		}`
	var test2Map map[string]interface{}
	err = json.Unmarshal([]byte(test2), &test2Map)
	assert.NoError(err)

	finalJSON, err = createConfigFileString(test2, tLogLevel, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	// the custom ones should be there...
	assert.Equal(controlMap["index-enabled"], false)
	assert.Equal(controlMap["public-ip"], "192.168.0.1")
	assert.Equal(controlMap["network-id"], float64(999))
	assert.Equal(controlMap["tx-fee"], float64(9999999))
	// ...as well as the common additional ones
	assert.Equal(controlMap["log-level"], tLogLevel)
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)
}
