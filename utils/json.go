// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import "encoding/json"

// Update the JSON body if the matching key is found
// and replace the value.
// e.g., "whitelisted-subnets" is the key and value is "a,b,c".
func UpdateJSONKey(jsonBody string, k string, v string) (string, error) {
	var config map[string]interface{}

	if err := json.Unmarshal([]byte(jsonBody), &config); err != nil {
		return "", err
	}

	if _, ok := config[k]; ok {
		if v == "" {
			delete(config, k)
		} else {
			config[k] = v
		}
	} else {
		// if the key wasn't found, no need to marshal again, just return original
		return jsonBody, nil
	}

	updatedJSON, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(updatedJSON), nil
}
