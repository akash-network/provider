package manifest

import (
	maniv2beta1 "pkg.akt.dev/go/manifest/v2beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
)

type submitRequest struct {
	Deployment dtypes.DeploymentID  `json:"deployment"`
	Manifest   maniv2beta1.Manifest `json:"manifest"`
}
