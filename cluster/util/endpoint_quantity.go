package util

import atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"

func GetEndpointQuantityOfResourceGroup(resources atypes.ResourceGroup, kind atypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	for _, resource := range resources.GetResources() {
		accumEndpointsOfResources(resource.Resources, kind, endpoints)

	}
	return uint(len(endpoints))
}

func accumEndpointsOfResources(r atypes.ResourceUnits, kind atypes.Endpoint_Kind, accum map[uint32]struct{}) {
	for _, endpoint := range r.Endpoints {
		if endpoint.Kind == kind {
			accum[endpoint.SequenceNumber] = struct{}{}
		}
	}
}

func GetEndpointQuantityOfResourceUnits(r atypes.ResourceUnits, kind atypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	accumEndpointsOfResources(r, kind, endpoints)

	return uint(len(endpoints))
}
