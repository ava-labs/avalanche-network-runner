// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import "encoding/json"

// Set k=v in JSON string
// e.g., "track-subnets" is the key and value is "a,b,c".
func SetJSONKey(jsonBody string, k string, v string) (string, error) {
	var config map[string]interface{}

	if err := json.Unmarshal([]byte(jsonBody), &config); err != nil {
		return "", err
	}

	if v == "" {
		delete(config, k)
	} else {
		config[k] = v
	}

	updatedJSON, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(updatedJSON), nil
}

func CombineJSONs(baseJson string, addedJson string) (string, error) {
	var baseConfig map[string]interface{}
	if err := json.Unmarshal([]byte(baseJson), &baseConfig); err != nil {
		return "", err
	}
	var addedConfig map[string]interface{}
	if err := json.Unmarshal([]byte(addedJson), &addedConfig); err != nil {
		return "", err
	}
	for k, v := range addedConfig {
		baseConfig[k] = v
	}
	updatedJSON, err := json.Marshal(baseConfig)
	if err != nil {
		return "", err
	}
	return string(updatedJSON), nil
}
