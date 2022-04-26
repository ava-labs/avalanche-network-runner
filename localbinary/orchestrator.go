// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package localbinary

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var (
	_ backend.NetworkOrchestrator = &orchestrator{}
	_ backend.NetworkConstructor  = &networkConstructor{}
	_ backend.Node                = &node{}
)

type orchestrator struct {
	lock sync.RWMutex

	orchestratorBaseDir string
	removeBaseDir       bool
	registry            backend.ExecutorRegistry
	networks            map[string]backend.Network
}

// NewNetworkOrchestrator creates a new orchestator that generates networks using processes started on the local machine
// If [wipeDir] is true, then the network orchestrator will attempt to wipe the contents of [baseDir] when Teardown is called.
func NewNetworkOrchestrator(baseDir string, registry backend.ExecutorRegistry, wipeDir bool) backend.NetworkOrchestrator {
	return &orchestrator{
		orchestratorBaseDir: baseDir,
		removeBaseDir:       wipeDir,
		registry:            registry,
		networks:            make(map[string]backend.Network),
	}
}

func (o *orchestrator) CreateNetwork(name string) (backend.Network, error) {
	o.lock.Lock()
	defer o.lock.Unlock()

	logrus.Infof("Creating network under name: %s", name)
	if _, exists := o.networks[name]; exists {
		return nil, fmt.Errorf("cannot create duplicate network under the name: %s", name)
	}

	constructor := newNetworkConstructor(filepath.Join(o.orchestratorBaseDir, name), o.registry)
	network := backend.NewNetwork(constructor)
	o.networks[name] = network
	return network, nil
}

func (o *orchestrator) GetNetwork(name string) (backend.Network, bool) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	network, exists := o.networks[name]
	return network, exists
}

func (o *orchestrator) Teardown(ctx context.Context) error {
	o.lock.Lock()
	defer o.lock.Unlock()

	logrus.Infof("Tearing down local network orchestrator...")
	eg := errgroup.Group{}
	for _, network := range o.networks {
		network := network
		eg.Go(func() error {
			return network.Teardown(ctx)
		})
	}

	errs := wrappers.Errs{}
	errs.Add(eg.Wait())
	if o.removeBaseDir {
		errs.Add(os.RemoveAll(o.orchestratorBaseDir))
	}
	return errs.Err
}
