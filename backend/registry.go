// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

import (
	"fmt"
	"sync"
)

var _ ExecutorRegistry = &executorRegistry{}

// ExecutorRegistry provides a simple interface for registering execution commands under a given name
// This is provided to a specific network orchestrator in order to tell it the correct executable command
// it can use to start a named process ie. v1.7.10, v1.7.9, modified client, etc.

type ExecutorRegistry interface {
	RegisterExecutor(name string, executor string) error
	GetExecutor(name string) (string, bool)
}

type executorRegistry struct {
	lock     sync.RWMutex
	registry map[string]string
}

func NewExecutorRegistry(registry map[string]string) ExecutorRegistry {
	return &executorRegistry{
		registry: registry,
	}
}

func NewEmptyExecutorRegistry() ExecutorRegistry {
	return &executorRegistry{
		registry: make(map[string]string),
	}
}

func (e *executorRegistry) RegisterExecutor(name, executor string) error {
	e.lock.Lock()
	defer e.lock.Unlock()

	_, exists := e.registry[name]
	if exists {
		return fmt.Errorf("cannot register duplicate executor under the name %s", name)
	}

	e.registry[name] = executor
	return nil
}

func (e *executorRegistry) GetExecutor(name string) (string, bool) {
	e.lock.RLock()
	defer e.lock.RUnlock()

	executor, ok := e.registry[name]
	return executor, ok
}
