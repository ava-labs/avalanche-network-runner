// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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
	lock    sync.RWMutex
	network NetworkConstructor

	nodes map[string]Node
}

func NewNetwork(constructor NetworkConstructor) Network {
	return &networkBackend{
		network: constructor,
		nodes:   make(map[string]Node),
	}
}

func (backend *networkBackend) GetNodes() []Node {
	backend.lock.RLock()
	defer backend.lock.RUnlock()

	nodes := make([]Node, 0, len(backend.nodes))
	for _, node := range backend.nodes {
		nodes = append(nodes, node)
	}

	return nodes
}

func (backend *networkBackend) GetNode(name string) (Node, bool) {
	backend.lock.RLock()
	defer backend.lock.RUnlock()

	node, exists := backend.nodes[name]
	return node, exists
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

func (backend *networkBackend) Teardown(ctx context.Context) error {
	return backend.network.Teardown(ctx)
}
