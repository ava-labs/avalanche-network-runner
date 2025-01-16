// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import (
	context "context"

	rpcpb "github.com/ava-labs/avalanche-network-runner/rpcpb"
	mock "github.com/stretchr/testify/mock"
)

// PingServiceServer is an autogenerated mock type for the PingServiceServer type
type PingServiceServer struct {
	mock.Mock
}

// Ping provides a mock function with given fields: _a0, _a1
func (_m *PingServiceServer) Ping(_a0 context.Context, _a1 *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for Ping")
	}

	var r0 *rpcpb.PingResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *rpcpb.PingRequest) (*rpcpb.PingResponse, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *rpcpb.PingRequest) *rpcpb.PingResponse); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*rpcpb.PingResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *rpcpb.PingRequest) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// mustEmbedUnimplementedPingServiceServer provides a mock function with no fields
func (_m *PingServiceServer) mustEmbedUnimplementedPingServiceServer() {
	_m.Called()
}

// NewPingServiceServer creates a new instance of PingServiceServer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewPingServiceServer(t interface {
	mock.TestingT
	Cleanup(func())
}) *PingServiceServer {
	mock := &PingServiceServer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
