// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/sirupsen/logrus"
)

var _ backend.OrchestratorBackend = &orchestrator{}

type orchestrator struct {
	orchestratorBaseDir string
	removeBaseDir       bool
	registry            backend.ExecutorRegistry
}

type OrchestratorConfig struct {
	BaseDir           string            `json:"baseDir"`
	Registry          map[string]string `json:"registry"`
	DestroyOnTeardown bool              `json:"destroyOnTeardown"`
}

func NewNetworkOrchestratorFromBytes(configBytes []byte) (backend.NetworkOrchestrator, error) {
	config := new(OrchestratorConfig)
	if err := json.Unmarshal(configBytes, config); err != nil {
		return nil, err
	}

	return NewNetworkOrchestrator(config), nil
}

// NewNetworkOrchestrator creates a new orchestator that generates networks using processes started on the local machine
// If [wipeDir] is true, then the network orchestrator will attempt to wipe the contents of [baseDir] when Teardown is called.
func NewNetworkOrchestrator(config *OrchestratorConfig) backend.NetworkOrchestrator {
	return backend.NewOrchestrator(&orchestrator{
		orchestratorBaseDir: config.BaseDir,
		removeBaseDir:       config.DestroyOnTeardown,
		registry:            backend.NewExecutorRegistry(config.Registry),
	})
}

func (o *orchestrator) CreateNetworkConstructor(name string) (backend.NetworkConstructor, error) {
	logrus.Infof("Creating network under name: %s", name)
	constructor := newNetworkConstructor(filepath.Join(o.orchestratorBaseDir, name), o.registry)
	return constructor, nil
}

func (o *orchestrator) Teardown(ctx context.Context) error {
	if o.removeBaseDir {
		return os.RemoveAll(o.orchestratorBaseDir)
	}
	return nil
}
