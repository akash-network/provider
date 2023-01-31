package v1beta2

import (
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
)

// Reservation interface implements orders and resources
type Reservation interface {
	OrderID() mtypes.OrderID
	Resources() atypes.ResourceGroup
	Allocated() bool
}
