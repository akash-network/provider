// Package errors provides cluster error types and HTTP status mapping.
//
// When returning cluster failures to the gateway, use NewClusterError or
// ClusterUnavailable so the gateway can map them to the correct HTTP status.
// Do not return raw errors for cluster failures.
package errors

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
)

// ClusterError carries an error and its HTTP status code for gateway responses.
// Use NewClusterError or ClusterUnavailable when returning cluster failures;
// do not return raw errors so the gateway can map them correctly.
var (
	errKubeClient                = errors.New("kube")
	ErrInternalError             = fmt.Errorf("%w: internal error", errKubeClient)
	ErrLeaseNotFound             = fmt.Errorf("%w: lease not found", errKubeClient)
	ErrNoDeploymentForLease      = fmt.Errorf("%w: no deployments for lease", errKubeClient)
	ErrNoManifestForLease        = fmt.Errorf("%w: no manifest for lease", errKubeClient)
	ErrNoServiceForLease         = fmt.Errorf("%w: no service for that lease", errKubeClient)
	ErrInvalidHostnameConnection = fmt.Errorf("%w: invalid hostname connection", errKubeClient)
	ErrNotConfiguredWithSettings = fmt.Errorf("%w: not configured with settings in the context passed to function", errKubeClient)
	ErrAlreadyExists             = fmt.Errorf("%w: resource already exists", errKubeClient)
)

type ClusterError struct {
	Err        error
	StatusCode int
}

func (e *ClusterError) Error() string {
	return e.Err.Error()
}

func (e *ClusterError) Unwrap() error {
	return e.Err
}

func NewClusterError(statusCode int, err error) *ClusterError {
	return &ClusterError{Err: err, StatusCode: statusCode}
}

func ClusterUnavailable(err error) *ClusterError {
	return NewClusterError(http.StatusServiceUnavailable, err)
}

func StatusCodeFrom(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var ce *ClusterError
	if errors.As(err, &ce) {
		return ce.StatusCode
	}
	if IsClusterUnavailable(err) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func WrapForGateway(err error) error {
	if err == nil {
		return nil
	}
	if IsClusterUnavailable(err) {
		return ClusterUnavailable(err)
	}
	return NewClusterError(http.StatusInternalServerError, err)
}

// IsClusterUnavailable reports whether err indicates the cluster is unreachable.
func IsClusterUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) || errors.Is(opErr.Err, syscall.ECONNRESET) {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "apiserver not ready") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.TrimSpace(msg) == "starting"
}
