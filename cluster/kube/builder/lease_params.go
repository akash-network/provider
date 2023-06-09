package builder

// type ParamsServices interface {
// 	builderBase
// 	Create() (*crd.LeaseParamsService, error)
// 	Update(obj *crd.LeaseParamsService) (*crd.LeaseParamsService, error)
// 	Params() crd.ParamsServices
// 	Name() string
// }
//
// type paramsServices struct {
// 	builder
// 	ns string
// }
//
// var _ ParamsServices = (*paramsServices)(nil)
//
// func BuildParamsLeases(log log.Logger, settings Settings, ns string, lid mtypes.LeaseID, group *mapi.Group) ParamsServices {
// 	params := make(crd.ParamsServices, 0, len(group.Services))
// 	for _, svc := range group.Services {
// 		sParams := crd.ParamsService{
// 			Name: svc.Name,
// 		}
//
// 		gpu := svc.Resources.GPU
// 		if gpu.Units.Value() > 0 {
// 			attrs, _ := ctypes.ParseGPUAttributes(gpu.Attributes)
// 			runtimeClass := ""
//
// 			if _, exists := attrs["nvidia"]; exists {
// 				runtimeClass = runtimeClassNvidia
// 			}
//
// 			sParams.Params = &crd.Params{
// 				RuntimeClass: runtimeClass,
// 				Affinity:     nil,
// 			}
// 		}
//
// 		params = append(params, sParams)
// 	}
//
// 	return &paramsServices{
// 		builder: builder{
// 			log:      log.With("module", "kube-builder"),
// 			settings: settings,
// 			lid:      lid,
// 			sparams:  params,
// 		},
// 		ns: ns,
// 	}
// }
//
// func (b *paramsServices) Create() (*crd.LeaseParamsService, error) {
// 	obj := &crd.LeaseParamsService{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Namespace: b.ns,
// 			Name:      b.Name(),
// 			Labels:    b.labels(),
// 		},
// 		Spec: crd.LeaseParamsServiceSpec{
// 			LeaseID:  crd.LeaseIDFromAkash(b.lid),
// 			Services: b.sparams,
// 		},
// 	}
//
// 	return obj, nil
// }
//
// func (b *paramsServices) Update(obj *crd.LeaseParamsService) (*crd.LeaseParamsService, error) {
// 	cobj := *obj
// 	cobj.Spec.Services = b.sparams
//
// 	return &cobj, nil
// }
//
// func (b *paramsServices) NS() string {
// 	return b.ns
// }
//
// func (b *paramsServices) Params() crd.ParamsServices {
// 	return b.sparams
// }
//
// func (b *paramsServices) labels() map[string]string {
// 	return AppendLeaseLabels(b.lid, b.builder.labels())
// }
