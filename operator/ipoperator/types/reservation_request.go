package types

import mtypes "github.com/akash-network/node/x/market/types/v1beta2"

type IPReservationRequest struct {
	OrderID  mtypes.OrderID
	Quantity uint
}
