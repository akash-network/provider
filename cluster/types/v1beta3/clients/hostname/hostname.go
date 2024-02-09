package hostname

import (
	"context"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type LeaseIDConnection interface {
	GetLeaseID() mtypes.LeaseID
	GetHostname() string
	GetExternalPort() int32
	GetServiceName() string
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
