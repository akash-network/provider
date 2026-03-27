// Package errors provides cluster error types.
package errors

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

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

// IsKubeAPIUnreachable reports whether err indicates the kube API server is unreachable.
func IsKubeAPIUnreachable(err error) bool {
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
		strings.TrimSpace(msg) == "starting" ||
		strings.Contains(msg, "connection refused") || // fallback for syscall.ECONNREFUSED
		strings.Contains(msg, "connection reset") // fallback for syscall.ECONNRESET

}
