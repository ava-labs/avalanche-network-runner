// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var _ Network = &networkBackend{}

// NetworkConstructor provides a thread safe interface for adding new nodes to a specific network
// Note: a NetworkConstructor is created as a network specific instance and used to implement a more
// feature complete Network backend without changing the fundamentals of the underlying network constructors.
type NetworkConstructor interface {
	AddNode(ctx context.Context, config NodeConfig) (Node, error)
	Teardown(ctx context.Context) error
}

type networkBackend struct {
	lock sync.RWMutex

	name    string
	network NetworkConstructor
	killed  bool

	removeNetwork func() error
	nodes         map[string]Node
}

func newNetwork(name string, constructor NetworkConstructor, removeNetwork func() error) Network {
	return &networkBackend{
		name:          name,
		network:       constructor,
		removeNetwork: removeNetwork,
		nodes:         make(map[string]Node),
	}
}

func (backend *networkBackend) GetName() string {
	return backend.name
}

func (backend *networkBackend) GetNodes() ([]Node, error) {
	backend.lock.RLock()
	defer backend.lock.RUnlock()

	nodes := make([]Node, 0, len(backend.nodes))
	for _, node := range backend.nodes {
		nodes = append(nodes, node)
	}

	return nodes, nil
}

func (backend *networkBackend) GetNode(name string) (Node, error) {
	backend.lock.RLock()
	defer backend.lock.RUnlock()

	node, exists := backend.nodes[name]
	if !exists {
		return nil, fmt.Errorf("cannot get non-existent node: %s", name)
	}
	return node, nil
}

func (backend *networkBackend) AddNode(ctx context.Context, config NodeConfig) (Node, error) {
	node, err := backend.network.AddNode(ctx, config)
	if err != nil {
		return nil, err
	}

	// Grab the lock after creating the node to allow parallelization on startup
	backend.lock.Lock()
	defer backend.lock.Unlock()

	_, exists := backend.nodes[config.Name]
	if exists {
		// Start a goroutine to shut down the node, so we don't need to block here while
		// holding the lock.
		// We're going to return the original source of the error anyways.
		go func() {
			if err := node.Stop(10 * time.Second); err != nil {
				logrus.Errorf("failed to stop node under duplicate name %s", config.Name)
			}
		}()
		return nil, fmt.Errorf("cannot create duplicate node under name: %s", config.Name)
	}

	backend.nodes[config.Name] = node
	return node, nil
}

func (backend *networkBackend) RemoveNode(name string, timeout time.Duration) error {
	backend.lock.Lock()
	defer backend.lock.Unlock()

	node, exists := backend.nodes[name]
	if !exists {
		return fmt.Errorf("cannot remove non-existent node: %s", name)
	}
	delete(backend.nodes, name)
	return node.Stop(timeout)
}

func (backend *networkBackend) Teardown(ctx context.Context) error {
	backend.lock.Lock()
	defer backend.lock.Unlock()

	// If the backend has already been torn down, consider this a no-op.
	if backend.killed {
		return nil
	}

	// Shut down all of the nodes in the network before calling teardown on the constructor
	eg := errgroup.Group{}
	for name, node := range backend.nodes {
		node := node
		eg.Go(func() error {
			return node.Stop(10 * time.Second)
		})
		// Remove the node from tracking after we have started a goroutine to kill it.
		// Note: if Stop fails the network will still be removed from the network tracking.
		delete(backend.nodes, name)
	}

	if err := eg.Wait(); err != nil {
		return err
	}
	if err := backend.network.Teardown(ctx); err != nil {
		return err
	}
	// Note: removeNetwork will grab a lock in the parent orchestrator. Therefore, we enforce the invariant that
	// we will always grab the backend lock prior to the orchestrator lock.
	//
	// To accomplish this, we simply never call any function on the network backend from the orchestrator while
	// holding the orchestrator lock.
	if err := backend.removeNetwork(); err != nil {
		return err
	}
	// Mark the backend as killed after it has successfully been taken down.
	backend.killed = true
	return nil
}
