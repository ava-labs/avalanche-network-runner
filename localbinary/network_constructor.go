// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/sirupsen/logrus"
)

var _ backend.NetworkConstructor = &networkConstructor{}

type networkConstructor struct {
	registry       backend.ExecutorRegistry
	networkBaseDir string
}

func newNetworkConstructor(networkBaseDir string, registry backend.ExecutorRegistry) backend.NetworkConstructor {
	return &networkConstructor{
		registry:       registry,
		networkBaseDir: networkBaseDir,
	}
}

func (c *networkConstructor) AddNode(ctx context.Context, nodeDef backend.NodeConfig) (backend.Node, error) {
	executable, exists := c.registry.GetExecutor(nodeDef.Executable)
	if !exists {
		return nil, fmt.Errorf("no executable found for node %s to execute command %s", nodeDef.Name, nodeDef.Executable)
	}

	// Modify the node config to advertise "127.0.0.1" as the public IP for the local network
	modifiedNodeConfig := backend.CopyConfig(nodeDef.Config)

	modifiedNodeConfig[config.PublicIPKey] = "127.0.0.1"

	// Find 2 free ports in case they are needed ie. the port keys are not explicitly set.
	ports, err := utils.GetFreePorts(2)
	if err != nil {
		return nil, fmt.Errorf("failed to find 2 free ports: %w", err)
	}
	if _, ok := modifiedNodeConfig[config.HTTPHostKey]; !ok {
		modifiedNodeConfig[config.HTTPHostKey] = ports[0]
	}
	if _, ok := modifiedNodeConfig[config.StakingPortKey]; !ok {
		modifiedNodeConfig[config.HTTPHostKey] = ports[1]
	}

	nodeConfigBytes, err := json.Marshal(modifiedNodeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal node config: %w", err)
	}

	logrus.Infof("Starting node %s with %s with config: \n%v\n", nodeDef.Name, nodeDef.Executable, string(nodeConfigBytes))
	cmdParams := []string{
		"./avalanchego",
		fmt.Sprintf("--%s=%s", config.ConfigContentKey, base64.StdEncoding.EncodeToString(nodeConfigBytes)),
		fmt.Sprintf("--%s=json", config.ConfigContentTypeKey),
	}

	// Seet $HOME to [networkBaseDir] so that the process will start with the base data directory as a sub-directory
	// of the network data directory.
	// TODO: switch from using HOME directory to a new AvalancheGo flag to set the base directory
	// TODO pipe stdout into a specific file or color/pipe it to normal stdout
	cmd := exec.Command(executable, cmdParams...)
	baseDataDir := filepath.Join(c.networkBaseDir, nodeDef.Name)
	cmd.Env = append(cmd.Env, fmt.Sprintf("HOME=%s", baseDataDir))
	node, err := newNode(cmd, nodeDef)
	if err != nil {
		return nil, err
	}
	return node, nil
}

// TODO: optionally remove associated data
func (c *networkConstructor) Teardown(ctx context.Context) error {
	return nil
}
