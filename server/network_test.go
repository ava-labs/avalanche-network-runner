package server

import (
	"encoding/json"
	"testing"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/stretchr/testify/assert"
)

func TestEvalConfig(t *testing.T) {
	assert := assert.New(t)

	// test using the default config...
	tLogDir := "/tmp/log"
	tDbDir := "/tmp/db"
	tWhitelistedSubnets := "someSubnet"
	tPluginDir := "/tmp/plugins"

	var defaultConfig, globalConfig map[string]interface{}

	defaultConfig = local.DefaultFlags

	config, err := mergeNodeConfig(defaultConfig, globalConfig, "")
	assert.NoError(err)
	finalJSON, err := createConfigFileString(config, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	var controlMap map[string]interface{}
	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	var test1Map map[string]interface{}
	test1Map = local.DefaultFlags
	// ...all default config entries should still be there...
	for k, v := range test1Map {
		assert.Equal(controlMap[k], v)
	}
	// ...as well as additional ones
	assert.Equal(controlMap["log-level"], defaultConfig["log-level"])
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)

	// now test a global provided config
	globalConfigJSON := `{
		"log-dir":"/home/user/logs",
		"db-dir":"/home/user/db",
		"plugin-dir":"/home/user/plugins",
		"log-display-level":"debug",
		"log-level":"debug",
		"whitelisted-subnets":"otherSubNets",
		"index-enabled":false,
		"public-ip":"192.168.0.1",
		"network-id":999,
		"http-port":777,
		"staking-port":555,
		"tx-fee":9999999
		}`

	err = json.Unmarshal([]byte(globalConfigJSON), &globalConfig)
	assert.NoError(err)

	config, err = mergeNodeConfig(defaultConfig, globalConfig, "")
	assert.NoError(err)
	finalJSON, err = createConfigFileString(config, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	// the custom ones should be there...
	assert.Equal(controlMap["index-enabled"], false)
	assert.Equal(controlMap["network-id"], float64(999))
	assert.Equal(controlMap["tx-fee"], float64(9999999))
	// ...as well as the common additional ones
	assert.Equal(controlMap["log-level"], "debug")
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)

	// these ones should be ignored as they are hard-set by the code and required by the runner
	assert.Equal(controlMap["public-ip"], "127.0.0.1")
	assert.NotEqual(controlMap["http-port"], 777)
	assert.NotEqual(controlMap["staking-port"], 555)
	// same test but as custom only - should have same effect
	customConfigJSON := globalConfigJSON
	config, err = mergeNodeConfig(defaultConfig, map[string]interface{}{}, customConfigJSON)
	assert.NoError(err)
	finalJSON, err = createConfigFileString(config, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	// the custom ones should be there...
	assert.Equal(controlMap["index-enabled"], false)
	assert.Equal(controlMap["network-id"], float64(999))
	assert.Equal(controlMap["tx-fee"], float64(9999999))
	// ...as well as the common additional ones
	assert.Equal(controlMap["log-level"], "debug")
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)
	// these ones should be ignored as they are hard-set by the code and required by the runner
	assert.Equal(controlMap["public-ip"], "127.0.0.1")
	assert.NotEqual(controlMap["http-port"], 777)
	assert.NotEqual(controlMap["staking-port"], 555)

	// finally a combined one with custom and global
	// newGlobalConfigJSON represents the global config, globalConfigJSON the custom. custom should override global
	newGlobalConfigJSON := `{
		"log-dir":"/home/user/logs",
		"db-dir":"/home/user/db",
		"plugin-dir":"/home/user/plugins",
		"log-display-level":"debug",
		"log-level":"info",
		"whitelisted-subnets":"otherSubNets",
		"index-enabled":false,
		"public-ip":"192.168.2.111",
		"network-id":888,
		"tx-fee":9999999,
		"staking-port":11111,
		"http-port":5555,
		"uptime-requirement":98.5
		}`
	err = json.Unmarshal([]byte(newGlobalConfigJSON), &globalConfig)
	assert.NoError(err)

	config, err = mergeNodeConfig(defaultConfig, globalConfig, customConfigJSON)
	assert.NoError(err)
	finalJSON, err = createConfigFileString(config, tLogDir, tDbDir, tPluginDir, tWhitelistedSubnets)
	assert.NoError(err)

	err = json.Unmarshal([]byte(finalJSON), &controlMap)
	assert.NoError(err)

	// the custom ones should be there...
	assert.Equal(controlMap["index-enabled"], false)
	assert.Equal(controlMap["network-id"], float64(999))
	assert.Equal(controlMap["tx-fee"], float64(9999999))
	// ...as well as the common additional ones
	assert.Equal(controlMap["log-level"], "debug")
	assert.Equal(controlMap["log-dir"], tLogDir)
	assert.Equal(controlMap["db-dir"], tDbDir)
	assert.Equal(controlMap["whitelisted-subnets"], tWhitelistedSubnets)
	// ...as well as the ones only in the global config
	assert.Equal(controlMap["uptime-requirement"], float64(98.5))
	// these ones should be ignored as they are hard-set by the code and required by the runner
	assert.Equal(controlMap["public-ip"], "127.0.0.1")
	assert.NotEqual(controlMap["staking-port"], float64(11111))
	assert.NotEqual(controlMap["http-port"], float64(5555))
}
