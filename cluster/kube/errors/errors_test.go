package errors

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestStatusCodeFrom(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 200},
		{"generic", errors.New("fail"), http.StatusInternalServerError},
		{"cluster_unavailable", ClusterUnavailable(errors.New("refused")), http.StatusServiceUnavailable},
		{"cluster_error_500", NewClusterError(500, errors.New("fail")), http.StatusInternalServerError},
		{"cluster_error_503", NewClusterError(503, errors.New("fail")), http.StatusServiceUnavailable},
		{"unwrapped_unavailable", errors.New("apiserver not ready"), http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, StatusCodeFrom(tt.err))
		})
	}
}

func TestWrapForGateway(t *testing.T) {
	base := errors.New("connection refused")
	wrapped := WrapForGateway(base)
	require.Contains(t, wrapped.Error(), "connection refused")
	require.Equal(t, http.StatusServiceUnavailable, StatusCodeFrom(wrapped))

	base2 := errors.New("other error")
	wrapped2 := WrapForGateway(base2)
	require.Equal(t, http.StatusInternalServerError, StatusCodeFrom(wrapped2))
}
