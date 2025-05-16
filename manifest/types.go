package manifest

import (
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"

	maniv2beta1 "github.com/akash-network/akash-api/go/manifest/v2beta2"
)

type submitRequest struct {
	Deployment dtypes.DeploymentID  `json:"deployment"`
	Manifest   maniv2beta1.Manifest `json:"manifest"`
}
