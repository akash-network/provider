package v1beta3

import (
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

type ReservationGroup interface {
	Resources() dtypes.ResourceGroup
	SetAllocatedResources(dtypes.ResourceUnits)
	GetAllocatedResources() dtypes.ResourceUnits
	SetClusterParams(interface{})
	ClusterParams() interface{}
}

// Reservation interface implements orders and resources
type Reservation interface {
	OrderID() mtypes.OrderID
	Allocated() bool
	ReservationGroup
}
