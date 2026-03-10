package errors

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/akash-network/provider/pkg/httperror"
)

func TestIsClusterUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic", errors.New("something failed"), false},
		{"apiserver_not_ready", errors.New("apiserver not ready"), true},
		{"connection_refused", &net.OpError{Err: syscall.ECONNREFUSED}, true},
		{"connection_reset", &net.OpError{Err: syscall.ECONNRESET}, true},
		{"connection_refused_string", errors.New("dial tcp: connection refused"), true},
		{"connection_reset_string", errors.New("read: connection reset by peer"), true},
		{"starting", errors.New("starting"), true},
		{"starting_with_newline", errors.New("starting\n"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, IsClusterUnavailable(tt.err))
		})
	}
}

func TestWrapClusterErrorForGateway(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{"connection_refused", errors.New("connection refused"), http.StatusServiceUnavailable},
		{"lease_not_found", ErrLeaseNotFound, http.StatusNotFound},
		{"no_deployment_for_lease", ErrNoDeploymentForLease, http.StatusNotFound},
		{"unknown", errors.New("other"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := WrapClusterErrorForGateway(tt.err)
			require.Equal(t, tt.expectedStatus, httperror.StatusCodeFrom(wrapped))
		})
	}
}
