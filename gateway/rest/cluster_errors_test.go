package rest

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/akash-network/provider/pkg/httperror"
)

func TestWrapClusterErrorToHTTP(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{"connection_refused", errors.New("connection refused"), http.StatusServiceUnavailable},
		{"lease_not_found", kubeclienterrors.ErrLeaseNotFound, http.StatusNotFound},
		{"no_deployment_for_lease", kubeclienterrors.ErrNoDeploymentForLease, http.StatusNotFound},
		{"unknown", errors.New("other"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := wrapClusterErrorToHTTP(tt.err)
			require.Equal(t, tt.expectedStatus, httperror.StatusCodeFrom(wrapped))
		})
	}
}
