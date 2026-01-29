package v1beta3

import (
	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

// IDeployment interface defined with LeaseID and ManifestGroup methods
type IDeployment interface {
	LeaseID() mtypes.LeaseID
	ManifestGroup() *maniv2beta2.Group
	ClusterParams() interface{}
	ResourceVersion() string
}

type Deployment struct {
	Lid         mtypes.LeaseID
	MGroup      *maniv2beta2.Group
	CParams     interface{}
	ResourceVer string
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

func (d *Deployment) ResourceVersion() string {
	return d.ResourceVer
}
