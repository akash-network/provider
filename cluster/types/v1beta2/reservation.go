package v1beta2

import (
	atypes "github.com/akash-network/node/types/v1beta2"
	mtypes "github.com/akash-network/node/x/market/types/v1beta2"
)

// Reservation interface implements orders and resources
type Reservation interface {
	OrderID() mtypes.OrderID
	Resources() atypes.ResourceGroup
	Allocated() bool
}
