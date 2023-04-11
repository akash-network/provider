package v1beta3

import (
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

// Reservation interface implements orders and resources
//
//go:generate mockery --name Reservation --output ../../mocks
type Reservation interface {
	OrderID() mtypes.OrderID
	Resources() atypes.ResourceGroup
	Allocated() bool
}
