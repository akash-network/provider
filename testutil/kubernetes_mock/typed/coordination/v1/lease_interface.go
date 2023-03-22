// Code generated by mockery v2.23.1. DO NOT EDIT.

package kubernetes_mocks

import (
	context "context"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mock "github.com/stretchr/testify/mock"

	types "k8s.io/apimachinery/pkg/types"

	v1 "k8s.io/client-go/applyconfigurations/coordination/v1"

	watch "k8s.io/apimachinery/pkg/watch"
)

// LeaseInterface is an autogenerated mock type for the LeaseInterface type
type LeaseInterface struct {
	mock.Mock
}

type LeaseInterface_Expecter struct {
	mock *mock.Mock
}

func (_m *LeaseInterface) EXPECT() *LeaseInterface_Expecter {
	return &LeaseInterface_Expecter{mock: &_m.Mock}
}

// Apply provides a mock function with given fields: ctx, lease, opts
func (_m *LeaseInterface) Apply(ctx context.Context, lease *v1.LeaseApplyConfiguration, opts metav1.ApplyOptions) (*coordinationv1.Lease, error) {
	ret := _m.Called(ctx, lease, opts)

	var r0 *coordinationv1.Lease
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *v1.LeaseApplyConfiguration, metav1.ApplyOptions) (*coordinationv1.Lease, error)); ok {
		return rf(ctx, lease, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *v1.LeaseApplyConfiguration, metav1.ApplyOptions) *coordinationv1.Lease); ok {
		r0 = rf(ctx, lease, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.Lease)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *v1.LeaseApplyConfiguration, metav1.ApplyOptions) error); ok {
		r1 = rf(ctx, lease, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Apply_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Apply'
type LeaseInterface_Apply_Call struct {
	*mock.Call
}

// Apply is a helper method to define mock.On call
//   - ctx context.Context
//   - lease *v1.LeaseApplyConfiguration
//   - opts metav1.ApplyOptions
func (_e *LeaseInterface_Expecter) Apply(ctx interface{}, lease interface{}, opts interface{}) *LeaseInterface_Apply_Call {
	return &LeaseInterface_Apply_Call{Call: _e.mock.On("Apply", ctx, lease, opts)}
}

func (_c *LeaseInterface_Apply_Call) Run(run func(ctx context.Context, lease *v1.LeaseApplyConfiguration, opts metav1.ApplyOptions)) *LeaseInterface_Apply_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*v1.LeaseApplyConfiguration), args[2].(metav1.ApplyOptions))
	})
	return _c
}

func (_c *LeaseInterface_Apply_Call) Return(result *coordinationv1.Lease, err error) *LeaseInterface_Apply_Call {
	_c.Call.Return(result, err)
	return _c
}

func (_c *LeaseInterface_Apply_Call) RunAndReturn(run func(context.Context, *v1.LeaseApplyConfiguration, metav1.ApplyOptions) (*coordinationv1.Lease, error)) *LeaseInterface_Apply_Call {
	_c.Call.Return(run)
	return _c
}

// Create provides a mock function with given fields: ctx, lease, opts
func (_m *LeaseInterface) Create(ctx context.Context, lease *coordinationv1.Lease, opts metav1.CreateOptions) (*coordinationv1.Lease, error) {
	ret := _m.Called(ctx, lease, opts)

	var r0 *coordinationv1.Lease
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *coordinationv1.Lease, metav1.CreateOptions) (*coordinationv1.Lease, error)); ok {
		return rf(ctx, lease, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *coordinationv1.Lease, metav1.CreateOptions) *coordinationv1.Lease); ok {
		r0 = rf(ctx, lease, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.Lease)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *coordinationv1.Lease, metav1.CreateOptions) error); ok {
		r1 = rf(ctx, lease, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Create_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Create'
type LeaseInterface_Create_Call struct {
	*mock.Call
}

// Create is a helper method to define mock.On call
//   - ctx context.Context
//   - lease *coordinationv1.Lease
//   - opts metav1.CreateOptions
func (_e *LeaseInterface_Expecter) Create(ctx interface{}, lease interface{}, opts interface{}) *LeaseInterface_Create_Call {
	return &LeaseInterface_Create_Call{Call: _e.mock.On("Create", ctx, lease, opts)}
}

func (_c *LeaseInterface_Create_Call) Run(run func(ctx context.Context, lease *coordinationv1.Lease, opts metav1.CreateOptions)) *LeaseInterface_Create_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*coordinationv1.Lease), args[2].(metav1.CreateOptions))
	})
	return _c
}

func (_c *LeaseInterface_Create_Call) Return(_a0 *coordinationv1.Lease, _a1 error) *LeaseInterface_Create_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *LeaseInterface_Create_Call) RunAndReturn(run func(context.Context, *coordinationv1.Lease, metav1.CreateOptions) (*coordinationv1.Lease, error)) *LeaseInterface_Create_Call {
	_c.Call.Return(run)
	return _c
}

// Delete provides a mock function with given fields: ctx, name, opts
func (_m *LeaseInterface) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	ret := _m.Called(ctx, name, opts)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, metav1.DeleteOptions) error); ok {
		r0 = rf(ctx, name, opts)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// LeaseInterface_Delete_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Delete'
type LeaseInterface_Delete_Call struct {
	*mock.Call
}

// Delete is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - opts metav1.DeleteOptions
func (_e *LeaseInterface_Expecter) Delete(ctx interface{}, name interface{}, opts interface{}) *LeaseInterface_Delete_Call {
	return &LeaseInterface_Delete_Call{Call: _e.mock.On("Delete", ctx, name, opts)}
}

func (_c *LeaseInterface_Delete_Call) Run(run func(ctx context.Context, name string, opts metav1.DeleteOptions)) *LeaseInterface_Delete_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(metav1.DeleteOptions))
	})
	return _c
}

func (_c *LeaseInterface_Delete_Call) Return(_a0 error) *LeaseInterface_Delete_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *LeaseInterface_Delete_Call) RunAndReturn(run func(context.Context, string, metav1.DeleteOptions) error) *LeaseInterface_Delete_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteCollection provides a mock function with given fields: ctx, opts, listOpts
func (_m *LeaseInterface) DeleteCollection(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions) error {
	ret := _m.Called(ctx, opts, listOpts)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, metav1.DeleteOptions, metav1.ListOptions) error); ok {
		r0 = rf(ctx, opts, listOpts)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// LeaseInterface_DeleteCollection_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteCollection'
type LeaseInterface_DeleteCollection_Call struct {
	*mock.Call
}

// DeleteCollection is a helper method to define mock.On call
//   - ctx context.Context
//   - opts metav1.DeleteOptions
//   - listOpts metav1.ListOptions
func (_e *LeaseInterface_Expecter) DeleteCollection(ctx interface{}, opts interface{}, listOpts interface{}) *LeaseInterface_DeleteCollection_Call {
	return &LeaseInterface_DeleteCollection_Call{Call: _e.mock.On("DeleteCollection", ctx, opts, listOpts)}
}

func (_c *LeaseInterface_DeleteCollection_Call) Run(run func(ctx context.Context, opts metav1.DeleteOptions, listOpts metav1.ListOptions)) *LeaseInterface_DeleteCollection_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(metav1.DeleteOptions), args[2].(metav1.ListOptions))
	})
	return _c
}

func (_c *LeaseInterface_DeleteCollection_Call) Return(_a0 error) *LeaseInterface_DeleteCollection_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *LeaseInterface_DeleteCollection_Call) RunAndReturn(run func(context.Context, metav1.DeleteOptions, metav1.ListOptions) error) *LeaseInterface_DeleteCollection_Call {
	_c.Call.Return(run)
	return _c
}

// Get provides a mock function with given fields: ctx, name, opts
func (_m *LeaseInterface) Get(ctx context.Context, name string, opts metav1.GetOptions) (*coordinationv1.Lease, error) {
	ret := _m.Called(ctx, name, opts)

	var r0 *coordinationv1.Lease
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, metav1.GetOptions) (*coordinationv1.Lease, error)); ok {
		return rf(ctx, name, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, metav1.GetOptions) *coordinationv1.Lease); ok {
		r0 = rf(ctx, name, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.Lease)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, metav1.GetOptions) error); ok {
		r1 = rf(ctx, name, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Get_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Get'
type LeaseInterface_Get_Call struct {
	*mock.Call
}

// Get is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - opts metav1.GetOptions
func (_e *LeaseInterface_Expecter) Get(ctx interface{}, name interface{}, opts interface{}) *LeaseInterface_Get_Call {
	return &LeaseInterface_Get_Call{Call: _e.mock.On("Get", ctx, name, opts)}
}

func (_c *LeaseInterface_Get_Call) Run(run func(ctx context.Context, name string, opts metav1.GetOptions)) *LeaseInterface_Get_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(metav1.GetOptions))
	})
	return _c
}

func (_c *LeaseInterface_Get_Call) Return(_a0 *coordinationv1.Lease, _a1 error) *LeaseInterface_Get_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *LeaseInterface_Get_Call) RunAndReturn(run func(context.Context, string, metav1.GetOptions) (*coordinationv1.Lease, error)) *LeaseInterface_Get_Call {
	_c.Call.Return(run)
	return _c
}

// List provides a mock function with given fields: ctx, opts
func (_m *LeaseInterface) List(ctx context.Context, opts metav1.ListOptions) (*coordinationv1.LeaseList, error) {
	ret := _m.Called(ctx, opts)

	var r0 *coordinationv1.LeaseList
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, metav1.ListOptions) (*coordinationv1.LeaseList, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, metav1.ListOptions) *coordinationv1.LeaseList); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.LeaseList)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, metav1.ListOptions) error); ok {
		r1 = rf(ctx, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_List_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'List'
type LeaseInterface_List_Call struct {
	*mock.Call
}

// List is a helper method to define mock.On call
//   - ctx context.Context
//   - opts metav1.ListOptions
func (_e *LeaseInterface_Expecter) List(ctx interface{}, opts interface{}) *LeaseInterface_List_Call {
	return &LeaseInterface_List_Call{Call: _e.mock.On("List", ctx, opts)}
}

func (_c *LeaseInterface_List_Call) Run(run func(ctx context.Context, opts metav1.ListOptions)) *LeaseInterface_List_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(metav1.ListOptions))
	})
	return _c
}

func (_c *LeaseInterface_List_Call) Return(_a0 *coordinationv1.LeaseList, _a1 error) *LeaseInterface_List_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *LeaseInterface_List_Call) RunAndReturn(run func(context.Context, metav1.ListOptions) (*coordinationv1.LeaseList, error)) *LeaseInterface_List_Call {
	_c.Call.Return(run)
	return _c
}

// Patch provides a mock function with given fields: ctx, name, pt, data, opts, subresources
func (_m *LeaseInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string) (*coordinationv1.Lease, error) {
	_va := make([]interface{}, len(subresources))
	for _i := range subresources {
		_va[_i] = subresources[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, name, pt, data, opts)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *coordinationv1.Lease
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*coordinationv1.Lease, error)); ok {
		return rf(ctx, name, pt, data, opts, subresources...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) *coordinationv1.Lease); ok {
		r0 = rf(ctx, name, pt, data, opts, subresources...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.Lease)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) error); ok {
		r1 = rf(ctx, name, pt, data, opts, subresources...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Patch_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Patch'
type LeaseInterface_Patch_Call struct {
	*mock.Call
}

// Patch is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - pt types.PatchType
//   - data []byte
//   - opts metav1.PatchOptions
//   - subresources ...string
func (_e *LeaseInterface_Expecter) Patch(ctx interface{}, name interface{}, pt interface{}, data interface{}, opts interface{}, subresources ...interface{}) *LeaseInterface_Patch_Call {
	return &LeaseInterface_Patch_Call{Call: _e.mock.On("Patch",
		append([]interface{}{ctx, name, pt, data, opts}, subresources...)...)}
}

func (_c *LeaseInterface_Patch_Call) Run(run func(ctx context.Context, name string, pt types.PatchType, data []byte, opts metav1.PatchOptions, subresources ...string)) *LeaseInterface_Patch_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]string, len(args)-5)
		for i, a := range args[5:] {
			if a != nil {
				variadicArgs[i] = a.(string)
			}
		}
		run(args[0].(context.Context), args[1].(string), args[2].(types.PatchType), args[3].([]byte), args[4].(metav1.PatchOptions), variadicArgs...)
	})
	return _c
}

func (_c *LeaseInterface_Patch_Call) Return(result *coordinationv1.Lease, err error) *LeaseInterface_Patch_Call {
	_c.Call.Return(result, err)
	return _c
}

func (_c *LeaseInterface_Patch_Call) RunAndReturn(run func(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*coordinationv1.Lease, error)) *LeaseInterface_Patch_Call {
	_c.Call.Return(run)
	return _c
}

// Update provides a mock function with given fields: ctx, lease, opts
func (_m *LeaseInterface) Update(ctx context.Context, lease *coordinationv1.Lease, opts metav1.UpdateOptions) (*coordinationv1.Lease, error) {
	ret := _m.Called(ctx, lease, opts)

	var r0 *coordinationv1.Lease
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *coordinationv1.Lease, metav1.UpdateOptions) (*coordinationv1.Lease, error)); ok {
		return rf(ctx, lease, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *coordinationv1.Lease, metav1.UpdateOptions) *coordinationv1.Lease); ok {
		r0 = rf(ctx, lease, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*coordinationv1.Lease)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *coordinationv1.Lease, metav1.UpdateOptions) error); ok {
		r1 = rf(ctx, lease, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Update_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Update'
type LeaseInterface_Update_Call struct {
	*mock.Call
}

// Update is a helper method to define mock.On call
//   - ctx context.Context
//   - lease *coordinationv1.Lease
//   - opts metav1.UpdateOptions
func (_e *LeaseInterface_Expecter) Update(ctx interface{}, lease interface{}, opts interface{}) *LeaseInterface_Update_Call {
	return &LeaseInterface_Update_Call{Call: _e.mock.On("Update", ctx, lease, opts)}
}

func (_c *LeaseInterface_Update_Call) Run(run func(ctx context.Context, lease *coordinationv1.Lease, opts metav1.UpdateOptions)) *LeaseInterface_Update_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*coordinationv1.Lease), args[2].(metav1.UpdateOptions))
	})
	return _c
}

func (_c *LeaseInterface_Update_Call) Return(_a0 *coordinationv1.Lease, _a1 error) *LeaseInterface_Update_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *LeaseInterface_Update_Call) RunAndReturn(run func(context.Context, *coordinationv1.Lease, metav1.UpdateOptions) (*coordinationv1.Lease, error)) *LeaseInterface_Update_Call {
	_c.Call.Return(run)
	return _c
}

// Watch provides a mock function with given fields: ctx, opts
func (_m *LeaseInterface) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	ret := _m.Called(ctx, opts)

	var r0 watch.Interface
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, metav1.ListOptions) (watch.Interface, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, metav1.ListOptions) watch.Interface); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(watch.Interface)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, metav1.ListOptions) error); ok {
		r1 = rf(ctx, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// LeaseInterface_Watch_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Watch'
type LeaseInterface_Watch_Call struct {
	*mock.Call
}

// Watch is a helper method to define mock.On call
//   - ctx context.Context
//   - opts metav1.ListOptions
func (_e *LeaseInterface_Expecter) Watch(ctx interface{}, opts interface{}) *LeaseInterface_Watch_Call {
	return &LeaseInterface_Watch_Call{Call: _e.mock.On("Watch", ctx, opts)}
}

func (_c *LeaseInterface_Watch_Call) Run(run func(ctx context.Context, opts metav1.ListOptions)) *LeaseInterface_Watch_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(metav1.ListOptions))
	})
	return _c
}

func (_c *LeaseInterface_Watch_Call) Return(_a0 watch.Interface, _a1 error) *LeaseInterface_Watch_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *LeaseInterface_Watch_Call) RunAndReturn(run func(context.Context, metav1.ListOptions) (watch.Interface, error)) *LeaseInterface_Watch_Call {
	_c.Call.Return(run)
	return _c
}

type mockConstructorTestingTNewLeaseInterface interface {
	mock.TestingT
	Cleanup(func())
}

// NewLeaseInterface creates a new instance of LeaseInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewLeaseInterface(t mockConstructorTestingTNewLeaseInterface) *LeaseInterface {
	mock := &LeaseInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
