package utils

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	coreth_params "github.com/ava-labs/coreth/params"
)

func LoadGenesisMap() (map[string]interface{}, error) {
	genesis, err := os.ReadFile(constants.LocalGenesisFile)
	if err != nil {
		panic(err)
	}
	var genesisMap map[string]interface{}
	if err = json.Unmarshal(genesis, &genesisMap); err != nil {
		panic(err)
	}

	cChainGenesis := genesisMap["cChainGenesis"]
	// set the cchain genesis directly from coreth
	// the whole of `cChainGenesis` should be set as a string, not a json object...
	corethCChainGenesis := coreth_params.AvalancheLocalChainConfig
	// but the part in coreth is only the "config" part.
	// In order to set it easily, first we get the cChainGenesis item
	// convert it to a map
	cChainGenesisMap, ok := cChainGenesis.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(
			"expected field 'cChainGenesis' of genesisMap to be a map[string]interface{}, but it failed with type %T", cChainGenesisMap)
	}
	// set the `config` key to the actual coreth object
	cChainGenesisMap["config"] = corethCChainGenesis
	// and then marshal everything into a string
	configBytes, err := json.Marshal(cChainGenesisMap)
	if err != nil {
		return nil, err
	}
	// this way the whole of `cChainGenesis` is a properly escaped string
	genesisMap["cChainGenesis"] = string(configBytes)
	return genesisMap, nil
}
