package manifest

import (
	"errors"
	"net/http"

	manifest "pkg.akt.dev/go/manifest/v2beta3"

	"github.com/akash-network/provider/pkg/httperror"
)

// WrapSubmitErrorForGateway wraps manifest submit errors with CustomError
// so the gateway can return the correct HTTP status.
func WrapSubmitErrorForGateway(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrManifestVersion):
		fallthrough
	case errors.Is(err, manifest.ErrInvalidManifest):
		fallthrough
	case errors.Is(err, manifest.ErrManifestCrossValidation):
		fallthrough
	case errors.Is(err, errManifestRejected):
		return httperror.NewError(http.StatusUnprocessableEntity, err)
	case errors.Is(err, ErrNoLeaseForDeployment):
		return httperror.NewError(http.StatusNotFound, err)
	default:
		return err
	}
}
