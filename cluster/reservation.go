package cluster

import (
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/util"
)

func newReservation(order mtypes.OrderID, resources dtypes.ResourceGroup) *reservation {
	return &reservation{
		order:            order,
		resources:        resources,
		endpointQuantity: util.GetEndpointQuantityOfResourceGroup(resources, atypes.Endpoint_LEASED_IP)}
}

type reservation struct {
	order             mtypes.OrderID
	resources         dtypes.ResourceGroup
	adjustedResources dtypes.ResourceUnits
	clusterParams     interface{}
	endpointQuantity  uint
	allocated         bool
	ipsConfirmed      bool
}

var _ ctypes.Reservation = (*reservation)(nil)

func (r *reservation) OrderID() mtypes.OrderID {
	return r.order
}

func (r *reservation) Resources() dtypes.ResourceGroup {
	return r.resources
}

func (r *reservation) SetAllocatedResources(val dtypes.ResourceUnits) {
	r.adjustedResources = val
}

func (r *reservation) GetAllocatedResources() dtypes.ResourceUnits {
	return r.adjustedResources
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
