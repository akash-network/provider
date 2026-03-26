package rest

import (
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/akash-network/provider/utils/httperror"
)

func TestClusterErrorToHTTP(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{"nil", nil, http.StatusOK},
		{"apiserver_not_ready", stderrors.New("apiserver not ready"), http.StatusServiceUnavailable},
		{"connection_refused", stderrors.New("connection refused"), http.StatusServiceUnavailable},
		{"lease_not_found", kubeclienterrors.ErrLeaseNotFound, http.StatusNotFound},
		{"no_deployment_for_lease", kubeclienterrors.ErrNoDeploymentForLease, http.StatusNotFound},
		{"no_manifest_for_lease", kubeclienterrors.ErrNoManifestForLease, http.StatusNotFound},
		{"no_service_for_lease", kubeclienterrors.ErrNoServiceForLease, http.StatusNotFound},
		{"k8s_not_found", kubeErrors.NewNotFound(schema.GroupResource{}, "lease"), http.StatusNotFound},
		{"unknown", stderrors.New("other"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := clusterErrorToHTTP(tt.err)
			require.Equal(t, tt.expectedStatus, httperror.StatusCodeFrom(wrapped))
		})
	}
}
