package v1beta3

import (
	maniv2beta2 "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
)

// Deployment interface defined with LeaseID and ManifestGroup methods
//
//go:generate mockery --name Deployment --output ../../mocks
type Deployment interface {
	LeaseID() mtypes.LeaseID
	ManifestGroup() maniv2beta2.Group
}

// // ParamsLease provider specific parameters required by deployment for correct operation
// type LeaseServiceParams struct {
// 	RuntimeClass string `json:"runtime_class"`
// }

// // Service extends manifest service with provider specific parameters
// type Service struct {
// 	maniv2beta2.Service `json:",inline"`
// 	ProviderParams      ServiceProviderParams `json:"provider_params"`
// }
//
// type Services []Service
//
// type Group struct {
// 	Name     string
// 	Services Services
// }
//
// type PGroup interface {
// 	maniv2beta2.IGroup
// 	ProviderGroup() *Group
// }
//
// var _ types.ResourceGroup = (*Group)(nil)
// var _ PGroup = (*Group)(nil)
//
// // GetName returns the name of group
// func (g Group) GetName() string {
// 	return g.Name
// }
//
// // GetResources returns list of resources in a group
// func (g Group) GetResources() []types.Resources {
// 	resources := make([]types.Resources, 0, len(g.Services))
// 	for _, s := range g.Services {
// 		resources = append(resources, types.Resources{
// 			Resources: s.Resources,
// 			Count:     s.Count,
// 		})
// 	}
//
// 	return resources
// }
//
// func (g Group) ProviderGroup() *Group {
// 	return &g
// }
//
// func (g Group) ManifestGroup() *maniv2beta2.Group {
// 	mgroup := &maniv2beta2.Group{
// 		Name:     g.Name,
// 		Services: make(maniv2beta2.Services, 0, len(g.Services)),
// 	}
//
// 	for _, svc := range g.Services {
// 		mgroup.Services = append(mgroup.Services, svc.Service)
// 	}
//
// 	return mgroup
// }
//
// func ProviderGroupFromManifest(mani *maniv2beta2.Group) *Group {
// 	g := &Group{
// 		Name:     mani.Name,
// 		Services: make(Services, 0, len(mani.Services)),
// 	}
//
// 	for _, svc := range mani.Services {
// 		g.Services = append(g.Services, Service{
// 			Service: svc,
// 		})
// 	}
//
// 	return g
// }
