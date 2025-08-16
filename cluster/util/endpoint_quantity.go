package util

import (
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

func GetEndpointQuantityOfResourceGroup(resources dtypes.ResourceGroup, kind rtypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	for _, resource := range resources.GetResourceUnits() {
		accumEndpointsOfResources(resource.Resources, kind, endpoints)

	}
	return uint(len(endpoints))
}

func accumEndpointsOfResources(r rtypes.Resources, kind rtypes.Endpoint_Kind, accum map[uint32]struct{}) {
	for _, endpoint := range r.Endpoints {
		if endpoint.Kind == kind {
			accum[endpoint.SequenceNumber] = struct{}{}
		}
	}
}

func GetEndpointQuantityOfResourceUnits(r rtypes.Resources, kind rtypes.Endpoint_Kind) uint {
	endpoints := make(map[uint32]struct{})
	accumEndpointsOfResources(r, kind, endpoints)

	return uint(len(endpoints))
}
