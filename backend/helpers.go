// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

func CopyConfig(config map[string]interface{}) map[string]interface{} {
	newConfig := make(map[string]interface{})
	for key, value := range config {
		newConfig[key] = value
	}

	return newConfig
}
