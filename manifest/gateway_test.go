package manifest

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	manifest "pkg.akt.dev/go/manifest/v2beta3"

	"github.com/akash-network/provider/pkg/httperror"
)

func TestWrapSubmitErrorForGateway(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{"nil", nil, http.StatusOK},
		{"err_manifest_version", ErrManifestVersion, http.StatusUnprocessableEntity},
		{"err_invalid_manifest", manifest.ErrInvalidManifest, http.StatusUnprocessableEntity},
		{"err_cross_validation", manifest.ErrManifestCrossValidation, http.StatusUnprocessableEntity},
		{"err_manifest_rejected", errManifestRejected, http.StatusUnprocessableEntity},
		{"err_no_lease_for_deployment", ErrNoLeaseForDeployment, http.StatusNotFound},
		{"unknown_error", errors.New("other"), http.StatusInternalServerError},
		{"wrapped_err_no_lease", errors.Join(ErrNoLeaseForDeployment, errors.New("ctx")), http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WrapSubmitErrorForGateway(tt.err)
			if tt.err == nil {
				require.NoError(t, got)
				return
			}
			require.Error(t, got)
			require.Equal(t, tt.expectedStatus, httperror.StatusCodeFrom(got))
		})
	}
}
