// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package constants

import "path/filepath"

const (
	LogNameMain            = "main"
	LogNameControl         = "control"
	LogNameTest            = "test"
	RootDirPrefix          = "network-runner-root-data"
	DefaultExecPathEnvVar  = "AVALANCHEGO_EXEC_PATH"
	DefaultPluginDirEnvVar = "AVALANCHEGO_PLUGIN_PATH"
)

var (
	LocalConfigDir   = filepath.Join("local", "default")
	LocalGenesisFile = "genesis.json"
)
