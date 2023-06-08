package cluster

import (
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/util"
)

func newReservation(order mtypes.OrderID, resources atypes.ResourceGroup) *reservation {
	return &reservation{
		order:            order,
		resources:        resources,
		endpointQuantity: util.GetEndpointQuantityOfResourceGroup(resources, atypes.Endpoint_LEASED_IP)}
}

type reservation struct {
	order             mtypes.OrderID
	resources         atypes.ResourceGroup
	adjustedResources []atypes.Resources
	clusterParams     interface{}
	endpointQuantity  uint
	allocated         bool
	ipsConfirmed      bool
}

var _ ctypes.Reservation = (*reservation)(nil)

func (r *reservation) OrderID() mtypes.OrderID {
	return r.order
}

func (r *reservation) Resources() atypes.ResourceGroup {
	return r.resources
}

func (r *reservation) SetAllocatedResources(val []atypes.Resources) {
	r.adjustedResources = val
}

func (r *reservation) SetClusterParams(val interface{}) {
	r.clusterParams = val
}

func (r *reservation) ClusterParams() interface{} {
	return r.clusterParams
}

func (r *reservation) Allocated() bool {
	return r.allocated
}
