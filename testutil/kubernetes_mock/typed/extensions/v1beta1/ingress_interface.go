// Code generated by mockery v2.23.1. DO NOT EDIT.

package kubernetes_mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"

	types "k8s.io/apimachinery/pkg/types"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1beta1 "k8s.io/client-go/applyconfigurations/extensions/v1beta1"

	watch "k8s.io/apimachinery/pkg/watch"
)

// IngressInterface is an autogenerated mock type for the IngressInterface type
type IngressInterface struct {
	mock.Mock
}

type IngressInterface_Expecter struct {
	mock *mock.Mock
}

func (_m *IngressInterface) EXPECT() *IngressInterface_Expecter {
	return &IngressInterface_Expecter{mock: &_m.Mock}
}

// Apply provides a mock function with given fields: ctx, ingress, opts
func (_m *IngressInterface) Apply(ctx context.Context, ingress *v1beta1.IngressApplyConfiguration, opts v1.ApplyOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, ingress, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, ingress, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, ingress, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) error); ok {
		r1 = rf(ctx, ingress, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Apply_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Apply'
type IngressInterface_Apply_Call struct {
	*mock.Call
}

// Apply is a helper method to define mock.On call
//   - ctx context.Context
//   - ingress *v1beta1.IngressApplyConfiguration
//   - opts v1.ApplyOptions
func (_e *IngressInterface_Expecter) Apply(ctx interface{}, ingress interface{}, opts interface{}) *IngressInterface_Apply_Call {
	return &IngressInterface_Apply_Call{Call: _e.mock.On("Apply", ctx, ingress, opts)}
}

func (_c *IngressInterface_Apply_Call) Run(run func(ctx context.Context, ingress *v1beta1.IngressApplyConfiguration, opts v1.ApplyOptions)) *IngressInterface_Apply_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*v1beta1.IngressApplyConfiguration), args[2].(v1.ApplyOptions))
	})
	return _c
}

func (_c *IngressInterface_Apply_Call) Return(result *extensionsv1beta1.Ingress, err error) *IngressInterface_Apply_Call {
	_c.Call.Return(result, err)
	return _c
}

func (_c *IngressInterface_Apply_Call) RunAndReturn(run func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_Apply_Call {
	_c.Call.Return(run)
	return _c
}

// ApplyStatus provides a mock function with given fields: ctx, ingress, opts
func (_m *IngressInterface) ApplyStatus(ctx context.Context, ingress *v1beta1.IngressApplyConfiguration, opts v1.ApplyOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, ingress, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, ingress, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, ingress, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) error); ok {
		r1 = rf(ctx, ingress, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_ApplyStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ApplyStatus'
type IngressInterface_ApplyStatus_Call struct {
	*mock.Call
}

// ApplyStatus is a helper method to define mock.On call
//   - ctx context.Context
//   - ingress *v1beta1.IngressApplyConfiguration
//   - opts v1.ApplyOptions
func (_e *IngressInterface_Expecter) ApplyStatus(ctx interface{}, ingress interface{}, opts interface{}) *IngressInterface_ApplyStatus_Call {
	return &IngressInterface_ApplyStatus_Call{Call: _e.mock.On("ApplyStatus", ctx, ingress, opts)}
}

func (_c *IngressInterface_ApplyStatus_Call) Run(run func(ctx context.Context, ingress *v1beta1.IngressApplyConfiguration, opts v1.ApplyOptions)) *IngressInterface_ApplyStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*v1beta1.IngressApplyConfiguration), args[2].(v1.ApplyOptions))
	})
	return _c
}

func (_c *IngressInterface_ApplyStatus_Call) Return(result *extensionsv1beta1.Ingress, err error) *IngressInterface_ApplyStatus_Call {
	_c.Call.Return(result, err)
	return _c
}

func (_c *IngressInterface_ApplyStatus_Call) RunAndReturn(run func(context.Context, *v1beta1.IngressApplyConfiguration, v1.ApplyOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_ApplyStatus_Call {
	_c.Call.Return(run)
	return _c
}

// Create provides a mock function with given fields: ctx, ingress, opts
func (_m *IngressInterface) Create(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.CreateOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, ingress, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.CreateOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, ingress, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.CreateOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, ingress, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *extensionsv1beta1.Ingress, v1.CreateOptions) error); ok {
		r1 = rf(ctx, ingress, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Create_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Create'
type IngressInterface_Create_Call struct {
	*mock.Call
}

// Create is a helper method to define mock.On call
//   - ctx context.Context
//   - ingress *extensionsv1beta1.Ingress
//   - opts v1.CreateOptions
func (_e *IngressInterface_Expecter) Create(ctx interface{}, ingress interface{}, opts interface{}) *IngressInterface_Create_Call {
	return &IngressInterface_Create_Call{Call: _e.mock.On("Create", ctx, ingress, opts)}
}

func (_c *IngressInterface_Create_Call) Run(run func(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.CreateOptions)) *IngressInterface_Create_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*extensionsv1beta1.Ingress), args[2].(v1.CreateOptions))
	})
	return _c
}

func (_c *IngressInterface_Create_Call) Return(_a0 *extensionsv1beta1.Ingress, _a1 error) *IngressInterface_Create_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_Create_Call) RunAndReturn(run func(context.Context, *extensionsv1beta1.Ingress, v1.CreateOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_Create_Call {
	_c.Call.Return(run)
	return _c
}

// Delete provides a mock function with given fields: ctx, name, opts
func (_m *IngressInterface) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	ret := _m.Called(ctx, name, opts)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, v1.DeleteOptions) error); ok {
		r0 = rf(ctx, name, opts)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// IngressInterface_Delete_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Delete'
type IngressInterface_Delete_Call struct {
	*mock.Call
}

// Delete is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - opts v1.DeleteOptions
func (_e *IngressInterface_Expecter) Delete(ctx interface{}, name interface{}, opts interface{}) *IngressInterface_Delete_Call {
	return &IngressInterface_Delete_Call{Call: _e.mock.On("Delete", ctx, name, opts)}
}

func (_c *IngressInterface_Delete_Call) Run(run func(ctx context.Context, name string, opts v1.DeleteOptions)) *IngressInterface_Delete_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(v1.DeleteOptions))
	})
	return _c
}

func (_c *IngressInterface_Delete_Call) Return(_a0 error) *IngressInterface_Delete_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *IngressInterface_Delete_Call) RunAndReturn(run func(context.Context, string, v1.DeleteOptions) error) *IngressInterface_Delete_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteCollection provides a mock function with given fields: ctx, opts, listOpts
func (_m *IngressInterface) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	ret := _m.Called(ctx, opts, listOpts)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, v1.DeleteOptions, v1.ListOptions) error); ok {
		r0 = rf(ctx, opts, listOpts)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// IngressInterface_DeleteCollection_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteCollection'
type IngressInterface_DeleteCollection_Call struct {
	*mock.Call
}

// DeleteCollection is a helper method to define mock.On call
//   - ctx context.Context
//   - opts v1.DeleteOptions
//   - listOpts v1.ListOptions
func (_e *IngressInterface_Expecter) DeleteCollection(ctx interface{}, opts interface{}, listOpts interface{}) *IngressInterface_DeleteCollection_Call {
	return &IngressInterface_DeleteCollection_Call{Call: _e.mock.On("DeleteCollection", ctx, opts, listOpts)}
}

func (_c *IngressInterface_DeleteCollection_Call) Run(run func(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions)) *IngressInterface_DeleteCollection_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(v1.DeleteOptions), args[2].(v1.ListOptions))
	})
	return _c
}

func (_c *IngressInterface_DeleteCollection_Call) Return(_a0 error) *IngressInterface_DeleteCollection_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *IngressInterface_DeleteCollection_Call) RunAndReturn(run func(context.Context, v1.DeleteOptions, v1.ListOptions) error) *IngressInterface_DeleteCollection_Call {
	_c.Call.Return(run)
	return _c
}

// Get provides a mock function with given fields: ctx, name, opts
func (_m *IngressInterface) Get(ctx context.Context, name string, opts v1.GetOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, name, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, v1.GetOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, name, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, v1.GetOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, name, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, v1.GetOptions) error); ok {
		r1 = rf(ctx, name, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Get_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Get'
type IngressInterface_Get_Call struct {
	*mock.Call
}

// Get is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - opts v1.GetOptions
func (_e *IngressInterface_Expecter) Get(ctx interface{}, name interface{}, opts interface{}) *IngressInterface_Get_Call {
	return &IngressInterface_Get_Call{Call: _e.mock.On("Get", ctx, name, opts)}
}

func (_c *IngressInterface_Get_Call) Run(run func(ctx context.Context, name string, opts v1.GetOptions)) *IngressInterface_Get_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(v1.GetOptions))
	})
	return _c
}

func (_c *IngressInterface_Get_Call) Return(_a0 *extensionsv1beta1.Ingress, _a1 error) *IngressInterface_Get_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_Get_Call) RunAndReturn(run func(context.Context, string, v1.GetOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_Get_Call {
	_c.Call.Return(run)
	return _c
}

// List provides a mock function with given fields: ctx, opts
func (_m *IngressInterface) List(ctx context.Context, opts v1.ListOptions) (*extensionsv1beta1.IngressList, error) {
	ret := _m.Called(ctx, opts)

	var r0 *extensionsv1beta1.IngressList
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, v1.ListOptions) (*extensionsv1beta1.IngressList, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, v1.ListOptions) *extensionsv1beta1.IngressList); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.IngressList)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, v1.ListOptions) error); ok {
		r1 = rf(ctx, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_List_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'List'
type IngressInterface_List_Call struct {
	*mock.Call
}

// List is a helper method to define mock.On call
//   - ctx context.Context
//   - opts v1.ListOptions
func (_e *IngressInterface_Expecter) List(ctx interface{}, opts interface{}) *IngressInterface_List_Call {
	return &IngressInterface_List_Call{Call: _e.mock.On("List", ctx, opts)}
}

func (_c *IngressInterface_List_Call) Run(run func(ctx context.Context, opts v1.ListOptions)) *IngressInterface_List_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(v1.ListOptions))
	})
	return _c
}

func (_c *IngressInterface_List_Call) Return(_a0 *extensionsv1beta1.IngressList, _a1 error) *IngressInterface_List_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_List_Call) RunAndReturn(run func(context.Context, v1.ListOptions) (*extensionsv1beta1.IngressList, error)) *IngressInterface_List_Call {
	_c.Call.Return(run)
	return _c
}

// Patch provides a mock function with given fields: ctx, name, pt, data, opts, subresources
func (_m *IngressInterface) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (*extensionsv1beta1.Ingress, error) {
	_va := make([]interface{}, len(subresources))
	for _i := range subresources {
		_va[_i] = subresources[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, name, pt, data, opts)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, types.PatchType, []byte, v1.PatchOptions, ...string) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, name, pt, data, opts, subresources...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, types.PatchType, []byte, v1.PatchOptions, ...string) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, name, pt, data, opts, subresources...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, types.PatchType, []byte, v1.PatchOptions, ...string) error); ok {
		r1 = rf(ctx, name, pt, data, opts, subresources...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Patch_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Patch'
type IngressInterface_Patch_Call struct {
	*mock.Call
}

// Patch is a helper method to define mock.On call
//   - ctx context.Context
//   - name string
//   - pt types.PatchType
//   - data []byte
//   - opts v1.PatchOptions
//   - subresources ...string
func (_e *IngressInterface_Expecter) Patch(ctx interface{}, name interface{}, pt interface{}, data interface{}, opts interface{}, subresources ...interface{}) *IngressInterface_Patch_Call {
	return &IngressInterface_Patch_Call{Call: _e.mock.On("Patch",
		append([]interface{}{ctx, name, pt, data, opts}, subresources...)...)}
}

func (_c *IngressInterface_Patch_Call) Run(run func(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string)) *IngressInterface_Patch_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]string, len(args)-5)
		for i, a := range args[5:] {
			if a != nil {
				variadicArgs[i] = a.(string)
			}
		}
		run(args[0].(context.Context), args[1].(string), args[2].(types.PatchType), args[3].([]byte), args[4].(v1.PatchOptions), variadicArgs...)
	})
	return _c
}

func (_c *IngressInterface_Patch_Call) Return(result *extensionsv1beta1.Ingress, err error) *IngressInterface_Patch_Call {
	_c.Call.Return(result, err)
	return _c
}

func (_c *IngressInterface_Patch_Call) RunAndReturn(run func(context.Context, string, types.PatchType, []byte, v1.PatchOptions, ...string) (*extensionsv1beta1.Ingress, error)) *IngressInterface_Patch_Call {
	_c.Call.Return(run)
	return _c
}

// Update provides a mock function with given fields: ctx, ingress, opts
func (_m *IngressInterface) Update(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.UpdateOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, ingress, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, ingress, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, ingress, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) error); ok {
		r1 = rf(ctx, ingress, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Update_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Update'
type IngressInterface_Update_Call struct {
	*mock.Call
}

// Update is a helper method to define mock.On call
//   - ctx context.Context
//   - ingress *extensionsv1beta1.Ingress
//   - opts v1.UpdateOptions
func (_e *IngressInterface_Expecter) Update(ctx interface{}, ingress interface{}, opts interface{}) *IngressInterface_Update_Call {
	return &IngressInterface_Update_Call{Call: _e.mock.On("Update", ctx, ingress, opts)}
}

func (_c *IngressInterface_Update_Call) Run(run func(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.UpdateOptions)) *IngressInterface_Update_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*extensionsv1beta1.Ingress), args[2].(v1.UpdateOptions))
	})
	return _c
}

func (_c *IngressInterface_Update_Call) Return(_a0 *extensionsv1beta1.Ingress, _a1 error) *IngressInterface_Update_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_Update_Call) RunAndReturn(run func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_Update_Call {
	_c.Call.Return(run)
	return _c
}

// UpdateStatus provides a mock function with given fields: ctx, ingress, opts
func (_m *IngressInterface) UpdateStatus(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.UpdateOptions) (*extensionsv1beta1.Ingress, error) {
	ret := _m.Called(ctx, ingress, opts)

	var r0 *extensionsv1beta1.Ingress
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) (*extensionsv1beta1.Ingress, error)); ok {
		return rf(ctx, ingress, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) *extensionsv1beta1.Ingress); ok {
		r0 = rf(ctx, ingress, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*extensionsv1beta1.Ingress)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) error); ok {
		r1 = rf(ctx, ingress, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_UpdateStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UpdateStatus'
type IngressInterface_UpdateStatus_Call struct {
	*mock.Call
}

// UpdateStatus is a helper method to define mock.On call
//   - ctx context.Context
//   - ingress *extensionsv1beta1.Ingress
//   - opts v1.UpdateOptions
func (_e *IngressInterface_Expecter) UpdateStatus(ctx interface{}, ingress interface{}, opts interface{}) *IngressInterface_UpdateStatus_Call {
	return &IngressInterface_UpdateStatus_Call{Call: _e.mock.On("UpdateStatus", ctx, ingress, opts)}
}

func (_c *IngressInterface_UpdateStatus_Call) Run(run func(ctx context.Context, ingress *extensionsv1beta1.Ingress, opts v1.UpdateOptions)) *IngressInterface_UpdateStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*extensionsv1beta1.Ingress), args[2].(v1.UpdateOptions))
	})
	return _c
}

func (_c *IngressInterface_UpdateStatus_Call) Return(_a0 *extensionsv1beta1.Ingress, _a1 error) *IngressInterface_UpdateStatus_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_UpdateStatus_Call) RunAndReturn(run func(context.Context, *extensionsv1beta1.Ingress, v1.UpdateOptions) (*extensionsv1beta1.Ingress, error)) *IngressInterface_UpdateStatus_Call {
	_c.Call.Return(run)
	return _c
}

// Watch provides a mock function with given fields: ctx, opts
func (_m *IngressInterface) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	ret := _m.Called(ctx, opts)

	var r0 watch.Interface
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, v1.ListOptions) (watch.Interface, error)); ok {
		return rf(ctx, opts)
	}
	if rf, ok := ret.Get(0).(func(context.Context, v1.ListOptions) watch.Interface); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(watch.Interface)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, v1.ListOptions) error); ok {
		r1 = rf(ctx, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IngressInterface_Watch_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Watch'
type IngressInterface_Watch_Call struct {
	*mock.Call
}

// Watch is a helper method to define mock.On call
//   - ctx context.Context
//   - opts v1.ListOptions
func (_e *IngressInterface_Expecter) Watch(ctx interface{}, opts interface{}) *IngressInterface_Watch_Call {
	return &IngressInterface_Watch_Call{Call: _e.mock.On("Watch", ctx, opts)}
}

func (_c *IngressInterface_Watch_Call) Run(run func(ctx context.Context, opts v1.ListOptions)) *IngressInterface_Watch_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(v1.ListOptions))
	})
	return _c
}

func (_c *IngressInterface_Watch_Call) Return(_a0 watch.Interface, _a1 error) *IngressInterface_Watch_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *IngressInterface_Watch_Call) RunAndReturn(run func(context.Context, v1.ListOptions) (watch.Interface, error)) *IngressInterface_Watch_Call {
	_c.Call.Return(run)
	return _c
}

type mockConstructorTestingTNewIngressInterface interface {
	mock.TestingT
	Cleanup(func())
}

// NewIngressInterface creates a new instance of IngressInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewIngressInterface(t mockConstructorTestingTNewIngressInterface) *IngressInterface {
	mock := &IngressInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
