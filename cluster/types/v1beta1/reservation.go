package v1beta1

import (
	atypes "github.com/akash-network/node/types/v1beta1"
	mtypes "github.com/akash-network/node/x/market/types/v1beta1"
)

// Reservation interface implements orders and resources
type Reservation interface {
	OrderID() mtypes.OrderID
	Resources() atypes.ResourceGroup
	Allocated() bool
}
