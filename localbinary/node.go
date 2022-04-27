// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanchego/config"
	"github.com/sirupsen/logrus"
)

var _ backend.Node = &node{}

type node struct {
	cmd *exec.Cmd

	config backend.NodeConfig

	httpBaseURI string
	bootstrapIP string

	nodeStopped chan struct{}
	stopErr     error
}

func newNode(cmd *exec.Cmd, nodeDef backend.NodeConfig) (*node, error) {
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start process for node %s: %w", nodeDef.Name, err)
	}
	node := &node{
		cmd:         cmd,
		config:      nodeDef,
		nodeStopped: make(chan struct{}),
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			logrus.Errorf("node %s stopped with error: %s", nodeDef.Name, err)
		} else {
			logrus.Debugf("node %s stopped.", nodeDef.Name)
		}

		node.stopErr = err
		close(node.nodeStopped)
	}()

	// Wait 500ms to optimistically try to ensure the node has started successfully.
	// If it fails within the first 500ms, return the error that occurs on startup.
	select {
	case <-node.nodeStopped:
		return nil, node.stopErr
	case <-time.After(500 * time.Millisecond):
	}

	// TODO use defaults from AvalancheGo for ports
	stakingPort := "9651"
	if val, ok := nodeDef.Config[config.StakingPortKey]; ok {
		stakingPort = fmt.Sprintf("%v", val)
	}
	node.bootstrapIP = fmt.Sprintf("127.0.0.1:%s", stakingPort)

	httpPort := "9650"
	if val, ok := nodeDef.Config[config.HTTPPortKey]; ok {
		httpPort = fmt.Sprintf("%v", val)
	}
	node.httpBaseURI = fmt.Sprintf("http://127.0.0.1:%s", httpPort)

	return node, nil
}

func (n *node) GetName() string { return n.config.Name }

func (n *node) GetHTTPBaseURI() string { return n.httpBaseURI }

func (n *node) GetBootstrapIP() string { return n.bootstrapIP }

func (n *node) Config() map[string]interface{} {
	return backend.CopyConfig(n.config.Config)
}

func (n *node) Stop(stopTimeout time.Duration) error {
	if err := n.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	// Attempt to wait for the process to stop before killing the process
	select {
	case <-n.nodeStopped:
		return n.stopErr
	case <-time.After(stopTimeout):
		return n.cmd.Process.Kill()
	}
}
