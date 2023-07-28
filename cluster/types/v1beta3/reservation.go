package v1beta3

import (
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
)

//go:generate mockery --name ReservationGroup --output ../../mocks
type ReservationGroup interface {
	Resources() dtypes.ResourceGroup
	SetAllocatedResources(dtypes.ResourceUnits)
	SetClusterParams(interface{})
	ClusterParams() interface{}
}

// Reservation interface implements orders and resources
//
//go:generate mockery --name Reservation --output ../../mocks
type Reservation interface {
	OrderID() mtypes.OrderID
	Allocated() bool
	ReservationGroup
}
