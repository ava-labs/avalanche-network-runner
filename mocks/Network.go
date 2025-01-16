// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import (
	context "context"

	ids "github.com/ava-labs/avalanchego/ids"
	mock "github.com/stretchr/testify/mock"

	network "github.com/ava-labs/avalanche-network-runner/network"

	node "github.com/ava-labs/avalanche-network-runner/network/node"
)

// Network is an autogenerated mock type for the Network type
type Network struct {
	mock.Mock
}

// AddNode provides a mock function with given fields: _a0
func (_m *Network) AddNode(_a0 node.Config) (node.Node, error) {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for AddNode")
	}

	var r0 node.Node
	var r1 error
	if rf, ok := ret.Get(0).(func(node.Config) (node.Node, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(node.Config) node.Node); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(node.Node)
		}
	}

	if rf, ok := ret.Get(1).(func(node.Config) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AddPermissionlessDelegators provides a mock function with given fields: _a0, _a1
func (_m *Network) AddPermissionlessDelegators(_a0 context.Context, _a1 []network.PermissionlessStakerSpec) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for AddPermissionlessDelegators")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.PermissionlessStakerSpec) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AddPermissionlessValidators provides a mock function with given fields: _a0, _a1
func (_m *Network) AddPermissionlessValidators(_a0 context.Context, _a1 []network.PermissionlessStakerSpec) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for AddPermissionlessValidators")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.PermissionlessStakerSpec) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AddSubnetValidators provides a mock function with given fields: _a0, _a1
func (_m *Network) AddSubnetValidators(_a0 context.Context, _a1 []network.SubnetValidatorsSpec) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for AddSubnetValidators")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.SubnetValidatorsSpec) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// CreateBlockchains provides a mock function with given fields: _a0, _a1
func (_m *Network) CreateBlockchains(_a0 context.Context, _a1 []network.BlockchainSpec) ([]ids.ID, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for CreateBlockchains")
	}

	var r0 []ids.ID
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.BlockchainSpec) ([]ids.ID, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []network.BlockchainSpec) []ids.ID); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]ids.ID)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []network.BlockchainSpec) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// CreateSubnets provides a mock function with given fields: _a0, _a1
func (_m *Network) CreateSubnets(_a0 context.Context, _a1 []network.SubnetSpec) ([]ids.ID, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for CreateSubnets")
	}

	var r0 []ids.ID
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.SubnetSpec) ([]ids.ID, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []network.SubnetSpec) []ids.ID); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]ids.ID)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []network.SubnetSpec) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetAllNodes provides a mock function with no fields
func (_m *Network) GetAllNodes() (map[string]node.Node, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetAllNodes")
	}

	var r0 map[string]node.Node
	var r1 error
	if rf, ok := ret.Get(0).(func() (map[string]node.Node, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() map[string]node.Node); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[string]node.Node)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetElasticSubnetID provides a mock function with given fields: _a0, _a1
func (_m *Network) GetElasticSubnetID(_a0 context.Context, _a1 ids.ID) (ids.ID, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for GetElasticSubnetID")
	}

	var r0 ids.ID
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, ids.ID) (ids.ID, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, ids.ID) ids.ID); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(ids.ID)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, ids.ID) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetLogRootDir provides a mock function with no fields
func (_m *Network) GetLogRootDir() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetLogRootDir")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetNetworkID provides a mock function with no fields
func (_m *Network) GetNetworkID() (uint32, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetNetworkID")
	}

	var r0 uint32
	var r1 error
	if rf, ok := ret.Get(0).(func() (uint32, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetNode provides a mock function with given fields: name
func (_m *Network) GetNode(name string) (node.Node, error) {
	ret := _m.Called(name)

	if len(ret) == 0 {
		panic("no return value specified for GetNode")
	}

	var r0 node.Node
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (node.Node, error)); ok {
		return rf(name)
	}
	if rf, ok := ret.Get(0).(func(string) node.Node); ok {
		r0 = rf(name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(node.Node)
		}
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetNodeNames provides a mock function with no fields
func (_m *Network) GetNodeNames() ([]string, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetNodeNames")
	}

	var r0 []string
	var r1 error
	if rf, ok := ret.Get(0).(func() ([]string, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() []string); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]string)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetRootDir provides a mock function with no fields
func (_m *Network) GetRootDir() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetRootDir")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetSnapshotNames provides a mock function with no fields
func (_m *Network) GetSnapshotNames() ([]string, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetSnapshotNames")
	}

	var r0 []string
	var r1 error
	if rf, ok := ret.Get(0).(func() ([]string, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() []string); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]string)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Healthy provides a mock function with given fields: _a0
func (_m *Network) Healthy(_a0 context.Context) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Healthy")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// PauseNode provides a mock function with given fields: ctx, name
func (_m *Network) PauseNode(ctx context.Context, name string) error {
	ret := _m.Called(ctx, name)

	if len(ret) == 0 {
		panic("no return value specified for PauseNode")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, name)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RemoveNode provides a mock function with given fields: ctx, name
func (_m *Network) RemoveNode(ctx context.Context, name string) error {
	ret := _m.Called(ctx, name)

	if len(ret) == 0 {
		panic("no return value specified for RemoveNode")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, name)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RemoveSnapshot provides a mock function with given fields: _a0, _a1
func (_m *Network) RemoveSnapshot(_a0 string, _a1 string) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for RemoveSnapshot")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string, string) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RemoveSubnetValidators provides a mock function with given fields: _a0, _a1
func (_m *Network) RemoveSubnetValidators(_a0 context.Context, _a1 []network.SubnetValidatorsSpec) error {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for RemoveSubnetValidators")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.SubnetValidatorsSpec) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RestartNode provides a mock function with given fields: _a0, _a1, _a2, _a3, _a4, _a5, _a6, _a7
func (_m *Network) RestartNode(_a0 context.Context, _a1 string, _a2 string, _a3 string, _a4 string, _a5 map[string]string, _a6 map[string]string, _a7 map[string]string) error {
	ret := _m.Called(_a0, _a1, _a2, _a3, _a4, _a5, _a6, _a7)

	if len(ret) == 0 {
		panic("no return value specified for RestartNode")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, string, map[string]string, map[string]string, map[string]string) error); ok {
		r0 = rf(_a0, _a1, _a2, _a3, _a4, _a5, _a6, _a7)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// ResumeNode provides a mock function with given fields: ctx, name
func (_m *Network) ResumeNode(ctx context.Context, name string) error {
	ret := _m.Called(ctx, name)

	if len(ret) == 0 {
		panic("no return value specified for ResumeNode")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, name)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SaveSnapshot provides a mock function with given fields: _a0, _a1, _a2, _a3
func (_m *Network) SaveSnapshot(_a0 context.Context, _a1 string, _a2 string, _a3 bool) (string, error) {
	ret := _m.Called(_a0, _a1, _a2, _a3)

	if len(ret) == 0 {
		panic("no return value specified for SaveSnapshot")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, bool) (string, error)); ok {
		return rf(_a0, _a1, _a2, _a3)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string, bool) string); ok {
		r0 = rf(_a0, _a1, _a2, _a3)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string, bool) error); ok {
		r1 = rf(_a0, _a1, _a2, _a3)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Stop provides a mock function with given fields: _a0
func (_m *Network) Stop(_a0 context.Context) error {
	ret := _m.Called(_a0)

	if len(ret) == 0 {
		panic("no return value specified for Stop")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// TransformSubnet provides a mock function with given fields: _a0, _a1
func (_m *Network) TransformSubnet(_a0 context.Context, _a1 []network.ElasticSubnetSpec) ([]ids.ID, []ids.ID, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for TransformSubnet")
	}

	var r0 []ids.ID
	var r1 []ids.ID
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, []network.ElasticSubnetSpec) ([]ids.ID, []ids.ID, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []network.ElasticSubnetSpec) []ids.ID); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]ids.ID)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []network.ElasticSubnetSpec) []ids.ID); ok {
		r1 = rf(_a0, _a1)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).([]ids.ID)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, []network.ElasticSubnetSpec) error); ok {
		r2 = rf(_a0, _a1)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// NewNetwork creates a new instance of Network. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewNetwork(t interface {
	mock.TestingT
	Cleanup(func())
}) *Network {
	mock := &Network{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
