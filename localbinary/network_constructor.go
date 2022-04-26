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
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type networkConstructor struct {
	lock sync.RWMutex

	registry       backend.ExecutorRegistry
	networkBaseDir string
	nodes          map[string]*node
}

func newNetworkConstructor(networkBaseDir string, registry backend.ExecutorRegistry) backend.NetworkConstructor {
	return &networkConstructor{
		registry:       registry,
		networkBaseDir: networkBaseDir,
		nodes:          make(map[string]*node),
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
	c.nodes[nodeDef.Name] = node
	return node, nil
}

func (c *networkConstructor) Teardown(ctx context.Context) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	eg := errgroup.Group{}
	for _, node := range c.nodes {
		node := node
		eg.Go(func() error {
			return node.Stop(10 * time.Second)
		})
	}

	errs := wrappers.Errs{}
	errs.Add(eg.Wait())
	return errs.Err
}
