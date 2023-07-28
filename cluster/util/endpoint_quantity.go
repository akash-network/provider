package util

import (
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

func GetEndpointQuantityOfResourceGroup(resources dtypes.ResourceGroup, kind atypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	for _, resource := range resources.GetResourceUnits() {
		accumEndpointsOfResources(resource.Resources, kind, endpoints)

	}
	return uint(len(endpoints))
}

func accumEndpointsOfResources(r atypes.Resources, kind atypes.Endpoint_Kind, accum map[uint32]struct{}) {
	for _, endpoint := range r.Endpoints {
		if endpoint.Kind == kind {
			accum[endpoint.SequenceNumber] = struct{}{}
		}
	}
}

func GetEndpointQuantityOfResourceUnits(r atypes.Resources, kind atypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	accumEndpointsOfResources(r, kind, endpoints)

	return uint(len(endpoints))
}
