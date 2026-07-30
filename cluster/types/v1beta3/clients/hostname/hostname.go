package hostname

import (
	"context"

	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type LeaseIDConnection interface {
	GetLeaseID() mtypes.LeaseID
	GetHostname() string
	GetExternalPort() int32
	GetServiceName() string
}

// LeaseIDHostnameConnection is a concrete implementation of LeaseIDConnection
// used by both the cluster client and hostname operator for tracking hostname
// to deployment connections.
type LeaseIDHostnameConnection struct {
	LeaseID      mtypes.LeaseID
	Hostname     string
	ExternalPort int32
	ServiceName  string
}

var _ LeaseIDConnection = LeaseIDHostnameConnection{}

func (lh LeaseIDHostnameConnection) GetHostname() string {
	return lh.Hostname
}

func (lh LeaseIDHostnameConnection) GetLeaseID() mtypes.LeaseID {
	return lh.LeaseID
}

func (lh LeaseIDHostnameConnection) GetExternalPort() int32 {
	return lh.ExternalPort
}

func (lh LeaseIDHostnameConnection) GetServiceName() string {
	return lh.ServiceName
}

type ResourceEvent interface {
	GetLeaseID() mtypes.LeaseID
	GetEventType() ctypes.ProviderResourceEvent
	GetHostname() string
	GetServiceName() string
	GetExternalPort() uint32
}

type Client interface {
	Check(ctx context.Context) error
	String() string
	Stop()
}

type ActiveHostname struct {
	ID       mtypes.LeaseID
	Hostname string
}

type ConnectToDeploymentDirective struct {
	Hostname    string
	LeaseID     mtypes.LeaseID
	ServiceName string
	ServicePort int32
	ReadTimeout uint32
	SendTimeout uint32
	NextTimeout uint32
	MaxBodySize uint32
	NextTries   uint32
	NextCases   []string
}
