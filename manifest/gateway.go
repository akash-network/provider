package manifest

import (
	"errors"
	"net/http"

	manifest "pkg.akt.dev/go/manifest/v2beta3"

	"github.com/akash-network/provider/pkg/httperror"
)

// ManifestSubmitErrorToHTTP maps manifest submit errors to HttpError for gateway responses.
func ManifestSubmitErrorToHTTP(err error) error {
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
		return httperror.NewHttpError(http.StatusUnprocessableEntity, err)
	case errors.Is(err, ErrNoLeaseForDeployment):
		return httperror.NewHttpError(http.StatusNotFound, err)
	default:
		return err
	}
}
