// Code generated by mockery v2.14.0. DO NOT EDIT.

package mocks

import (
	context "context"

	health "github.com/ava-labs/avalanchego/api/health"
	mock "github.com/stretchr/testify/mock"

	rpc "github.com/ava-labs/avalanchego/utils/rpc"

	time "time"
)

// HealthClient is an autogenerated mock type for the Client type
type HealthClient struct {
	mock.Mock
}

// AwaitHealthy provides a mock function with given fields: ctx, freq, options
func (_m *HealthClient) AwaitHealthy(ctx context.Context, freq time.Duration, options ...rpc.Option) (bool, error) {
	_va := make([]interface{}, len(options))
	for _i := range options {
		_va[_i] = options[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, freq)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 bool
	if rf, ok := ret.Get(0).(func(context.Context, time.Duration, ...rpc.Option) bool); ok {
		r0 = rf(ctx, freq, options...)
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, time.Duration, ...rpc.Option) error); ok {
		r1 = rf(ctx, freq, options...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Health provides a mock function with given fields: _a0, _a1
func (_m *HealthClient) Health(_a0 context.Context, _a1 ...rpc.Option) (*health.APIHealthReply, error) {
	_va := make([]interface{}, len(_a1))
	for _i := range _a1 {
		_va[_i] = _a1[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _a0)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *health.APIHealthReply
	if rf, ok := ret.Get(0).(func(context.Context, ...rpc.Option) *health.APIHealthReply); ok {
		r0 = rf(_a0, _a1...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*health.APIHealthReply)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, ...rpc.Option) error); ok {
		r1 = rf(_a0, _a1...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Liveness provides a mock function with given fields: _a0, _a1
func (_m *HealthClient) Liveness(_a0 context.Context, _a1 ...rpc.Option) (*health.APIHealthReply, error) {
	_va := make([]interface{}, len(_a1))
	for _i := range _a1 {
		_va[_i] = _a1[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _a0)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *health.APIHealthReply
	if rf, ok := ret.Get(0).(func(context.Context, ...rpc.Option) *health.APIHealthReply); ok {
		r0 = rf(_a0, _a1...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*health.APIHealthReply)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, ...rpc.Option) error); ok {
		r1 = rf(_a0, _a1...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Readiness provides a mock function with given fields: _a0, _a1
func (_m *HealthClient) Readiness(_a0 context.Context, _a1 ...rpc.Option) (*health.APIHealthReply, error) {
	_va := make([]interface{}, len(_a1))
	for _i := range _a1 {
		_va[_i] = _a1[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _a0)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *health.APIHealthReply
	if rf, ok := ret.Get(0).(func(context.Context, ...rpc.Option) *health.APIHealthReply); ok {
		r0 = rf(_a0, _a1...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*health.APIHealthReply)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, ...rpc.Option) error); ok {
		r1 = rf(_a0, _a1...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

type mockConstructorTestingTNewHealthClient interface {
	mock.TestingT
	Cleanup(func())
}

// NewHealthClient creates a new instance of HealthClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewHealthClient(t mockConstructorTestingTNewHealthClient) *HealthClient {
	mock := &HealthClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
