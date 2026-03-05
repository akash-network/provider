package errors

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

var (
	ErrKubeClient                = errors.New("kube")
	ErrInternalError             = fmt.Errorf("%w: internal error", ErrKubeClient)
	ErrLeaseNotFound             = fmt.Errorf("%w: lease not found", ErrKubeClient)
	ErrNoDeploymentForLease      = fmt.Errorf("%w: no deployments for lease", ErrKubeClient)
	ErrNoManifestForLease        = fmt.Errorf("%w: no manifest for lease", ErrKubeClient)
	ErrNoServiceForLease         = fmt.Errorf("%w: no service for that lease", ErrKubeClient)
	ErrInvalidHostnameConnection = fmt.Errorf("%w: invalid hostname connection", ErrKubeClient)
	ErrNotConfiguredWithSettings = fmt.Errorf("%w: not configured with settings in the context passed to function", ErrKubeClient)
	ErrAlreadyExists             = fmt.Errorf("%w: resource already exists", ErrKubeClient)
)

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
