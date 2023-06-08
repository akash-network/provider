package v1beta3

import (
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

//go:generate mockery --name ReservationGroup --output ../../mocks
type ReservationGroup interface {
	Resources() atypes.ResourceGroup
	SetAllocatedResources([]atypes.Resources)
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
