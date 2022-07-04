package manifest

import (
	dtypes "github.com/ovrclk/akash/x/deployment/types/v1beta2"

	maniv2beta1 "github.com/ovrclk/akash/manifest/v2beta1"
)

// Status is the data structure
type Status struct {
	Deployments uint32 `json:"deployments"`
}

type submitRequest struct {
	Deployment dtypes.DeploymentID  `json:"deployment"`
	Manifest   maniv2beta1.Manifest `json:"manifest"`
}
