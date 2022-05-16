// Code generated by mockery v2.10.6. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	network "github.com/ava-labs/avalanche-network-runner/network"
)

// NodeProcess is an autogenerated mock type for the NodeProcess type
type NodeProcess struct {
	mock.Mock
}

// Alive provides a mock function with given fields:
func (_m *NodeProcess) Alive() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// Start provides a mock function with given fields: _a0
func (_m *NodeProcess) Start(_a0 chan network.UnexpectedStopMsg) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(chan network.UnexpectedStopMsg) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Stop provides a mock function with given fields:
func (_m *NodeProcess) Stop() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Wait provides a mock function with given fields: ctx
func (_m *NodeProcess) Wait(ctx context.Context) error {
	ret := _m.Called(ctx)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
