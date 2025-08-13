package ip

import (
	"context"
	"errors"
	"fmt"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

type ClusterIPPassthroughDirective struct {
	LeaseID      mtypes.LeaseID
	ServiceName  string
	Port         uint32
	ExternalPort uint32
	SharingKey   string
	Protocol     manifest.ServiceProtocol
}

type ResourceEvent interface {
	GetLeaseID() mtypes.LeaseID
	GetServiceName() string
	GetExternalPort() uint32
	GetPort() uint32
	GetSharingKey() string
	GetProtocol() manifest.ServiceProtocol
	GetEventType() ctypes.ProviderResourceEvent
}

type Passthrough interface {
	GetLeaseID() mtypes.LeaseID
	GetServiceName() string
	GetExternalPort() uint32
	GetPort() uint32
	GetSharingKey() string
	GetProtocol() manifest.ServiceProtocol
}

type LeaseState interface {
	Passthrough
	GetIP() string
}

type AddressUsage struct {
	Available uint
	InUse     uint
}

type LeaseIPStatus struct {
	Port         uint32
	ExternalPort uint32
	ServiceName  string
	IP           string
	Protocol     string
}

type ReservationRequest struct {
	OrderID  mtypes.OrderID
	Quantity uint
}

type ReservationDelete struct {
	OrderID mtypes.OrderID
}

type Client interface {
	Check(context.Context) error
	GetIPAddressUsage(context.Context) (AddressUsage, error)
	GetIPAddressStatus(context.Context, mtypes.OrderID) ([]LeaseIPStatus, error)
	String() string
	Stop()
}

/* A null client for use in tests and other scenarios */
type nullClient struct{}

var (
	_                 Client = (*nullClient)(nil)
	errNotImplemented        = errors.New("not implemented")
)

func NewNullClient() Client {
	return nullClient{}
}

func (v nullClient) String() string {
	return fmt.Sprintf("<%T>", v)
}

func (nullClient) Check(_ context.Context) error {
	return errNotImplemented
}

func (nullClient) GetIPAddressUsage(_ context.Context) (AddressUsage, error) {
	return AddressUsage{}, errNotImplemented
}

func (nullClient) Stop() {}

func (nullClient) GetIPAddressStatus(context.Context, mtypes.OrderID) ([]LeaseIPStatus, error) {
	return nil, errNotImplemented
}
