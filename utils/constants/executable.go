// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package constants

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	NormalExecution             = "NormalExecution"
	DockerRepo                  = "avaplatform"
	DockerImageName             = "avalanchego"
	DockerImageTag              = "v1.7.10"
	AvalancheGoBinaryPathEnvKey = "AVALANCHEGO_BINARY_PATH"
)

var (
	AvalancheGoDockerImage = fmt.Sprintf("%s/%s:%s", DockerRepo, DockerImageName, DockerImageTag)
	AvalancheGoBinary      = filepath.Join(os.ExpandEnv("$GOPATH"), "src", "github.com", "ava-labs", "avalanchego", "build", "avalanchego")
	BaseDataDir            = filepath.Join(os.ExpandEnv("$HOME"), ".avalanche-network-runner")
)

func init() {
	// Override AvalancheGoBinary if the environment variable is defined
	if binaryPath, exists := os.LookupEnv(AvalancheGoBinaryPathEnvKey); exists {
		AvalancheGoBinary = binaryPath
	}
}
