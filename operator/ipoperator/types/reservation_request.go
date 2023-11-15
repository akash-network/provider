package types

import mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

type IPReservationRequest struct {
	OrderID  mtypes.OrderID
	Quantity uint
}
