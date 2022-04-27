// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

import (
	"context"
	"fmt"
	"sync"
)

var _ NetworkOrchestrator = &orchestrator{}

type OrchestratorBackend interface {
	CreateNetworkConstructor(name string) (NetworkConstructor, error)
	Teardown(ctx context.Context) error
}

type orchestrator struct {
	// invariant: never call a function on one of the networks while holding this lock.
	lock sync.RWMutex

	networks map[string]Network
	backend  OrchestratorBackend
}

func NewOrchestrator(backend OrchestratorBackend) NetworkOrchestrator {
	return &orchestrator{
		networks: make(map[string]Network),
		backend:  backend,
	}
}

func (o *orchestrator) CreateNetwork(name string) (Network, error) {
	o.lock.Lock()
	defer o.lock.Unlock()

	_, exists := o.networks[name]
	if exists {
		return nil, fmt.Errorf("cannot create duplicate network under name: %s", name)
	}

	networkConstructor, err := o.backend.CreateNetworkConstructor(name)
	if err != nil {
		return nil, err
	}

	network := newNetwork(name, networkConstructor, func() error {
		_, err := o.removeNetwork(name)
		return err
	})
	o.networks[name] = network
	return network, nil
}

func (o *orchestrator) GetNetworks() ([]Network, error) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	networks := make([]Network, 0, len(o.networks))
	for _, network := range o.networks {
		networks = append(networks, network)
	}
	return networks, nil
}

func (o *orchestrator) GetNetwork(name string) (Network, error) {
	o.lock.RLock()
	defer o.lock.RUnlock()

	network, exists := o.networks[name]
	if !exists {
		return nil, fmt.Errorf("cannot get non-existent network: %s", name)
	}

	return network, nil
}

func (o *orchestrator) TeardownNetwork(ctx context.Context, name string) error {
	network, err := o.GetNetwork(name)
	if err != nil {
		return err
	}

	return network.Teardown(ctx)
}

// removeNetwork grabs the lock to remove the network under [name] from the networks map and returns
// the removed network or an error if it can't be found.
func (o *orchestrator) removeNetwork(name string) (Network, error) {
	o.lock.Lock()
	defer o.lock.Unlock()

	network, exists := o.networks[name]
	if !exists {
		return nil, fmt.Errorf("cannot teardown non-existent network: %s", name)
	}
	delete(o.networks, name)
	return network, nil
}

// Teardown calls teardown on the underlying backend and is responsible for cleaning up everything when the orchestrator
// shuts down.
// Requires that all of the networks have already been torn down.
func (o *orchestrator) Teardown(ctx context.Context) error {
	o.lock.Lock()
	defer o.lock.Unlock()

	if len(o.networks) > 0 {
		return fmt.Errorf("cannot teardown orchestrator with %d active networks", len(o.networks))
	}
	return o.backend.Teardown(ctx)
}
