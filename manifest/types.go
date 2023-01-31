package manifest

import (
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"

	maniv2beta1 "github.com/akash-network/akash-api/go/manifest/v2beta2"
)

// Status is the data structure
type Status struct {
	Deployments uint32 `json:"deployments"`
}

type submitRequest struct {
	Deployment dtypes.DeploymentID  `json:"deployment"`
	Manifest   maniv2beta1.Manifest `json:"manifest"`
}
