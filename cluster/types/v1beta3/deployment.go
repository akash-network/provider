package v1beta3

import (
	maniv2beta2 "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
)

// IDeployment interface defined with LeaseID and ManifestGroup methods
//
//go:generate mockery --name IDeployment --output ./mocks
type IDeployment interface {
	LeaseID() mtypes.LeaseID
	ManifestGroup() *maniv2beta2.Group
	ClusterParams() interface{}
}

type Deployment struct {
	Lid     mtypes.LeaseID
	MGroup  *maniv2beta2.Group
	CParams interface{}
}

var _ IDeployment = (*Deployment)(nil)

func (d *Deployment) LeaseID() mtypes.LeaseID {
	return d.Lid
}

func (d *Deployment) ManifestGroup() *maniv2beta2.Group {
	return d.MGroup
}

func (d *Deployment) ClusterParams() interface{} {
	return d.CParams
}
