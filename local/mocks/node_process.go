// Code generated by mockery v2.20.2. DO NOT EDIT.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	status "github.com/ava-labs/avalanche-network-runner/network/node/status"
)

// NodeProcess is an autogenerated mock type for the NodeProcess type
type NodeProcess struct {
	mock.Mock
}

// Status provides a mock function with given fields:
func (_m *NodeProcess) Status() status.Status {
	ret := _m.Called()

	var r0 status.Status
	if rf, ok := ret.Get(0).(func() status.Status); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(status.Status)
	}

	return r0
}

// Stop provides a mock function with given fields: ctx
func (_m *NodeProcess) Stop(ctx context.Context) int {
	ret := _m.Called(ctx)

	var r0 int
	if rf, ok := ret.Get(0).(func(context.Context) int); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(int)
	}

	return r0
}

type mockConstructorTestingTNewNodeProcess interface {
	mock.TestingT
	Cleanup(func())
}

// NewNodeProcess creates a new instance of NodeProcess. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewNodeProcess(t mockConstructorTestingTNewNodeProcess) *NodeProcess {
	mock := &NodeProcess{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
