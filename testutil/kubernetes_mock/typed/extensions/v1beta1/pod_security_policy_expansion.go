// Code generated by mockery v2.23.1. DO NOT EDIT.

package kubernetes_mocks

import mock "github.com/stretchr/testify/mock"

// PodSecurityPolicyExpansion is an autogenerated mock type for the PodSecurityPolicyExpansion type
type PodSecurityPolicyExpansion struct {
	mock.Mock
}

type PodSecurityPolicyExpansion_Expecter struct {
	mock *mock.Mock
}

func (_m *PodSecurityPolicyExpansion) EXPECT() *PodSecurityPolicyExpansion_Expecter {
	return &PodSecurityPolicyExpansion_Expecter{mock: &_m.Mock}
}

type mockConstructorTestingTNewPodSecurityPolicyExpansion interface {
	mock.TestingT
	Cleanup(func())
}

// NewPodSecurityPolicyExpansion creates a new instance of PodSecurityPolicyExpansion. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewPodSecurityPolicyExpansion(t mockConstructorTestingTNewPodSecurityPolicyExpansion) *PodSecurityPolicyExpansion {
	mock := &PodSecurityPolicyExpansion{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
