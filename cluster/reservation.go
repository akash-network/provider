package cluster

import (
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/util"
)

func newReservation(order mtypes.OrderID, resources dtypes.ResourceGroup) *reservation {
	return &reservation{
		order:            order,
		resources:        resources,
		endpointQuantity: util.GetEndpointQuantityOfResourceGroup(resources, rtypes.Endpoint_LEASED_IP)}
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
