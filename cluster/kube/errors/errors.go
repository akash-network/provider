// Package errors provides cluster error types and HTTP status mapping.
//
// The gateway wraps cluster errors via WrapClusterErrorForGateway before writing
// HTTP responses. The cluster client returns raw errors.
package errors

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"

	kubeErrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/akash-network/provider/pkg/httperror"
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

// WrapClusterErrorForGateway wraps a cluster error in a custom HttpError with the appropriate HTTP status code.
func WrapClusterErrorForGateway(err error) error {
	switch {
	case err == nil:
		return nil
	case IsClusterUnavailable(err):
		return httperror.NewHttpError(http.StatusServiceUnavailable, err)
	case errors.Is(err, ErrNoDeploymentForLease):
		fallthrough
	case errors.Is(err, ErrLeaseNotFound):
		fallthrough
	case errors.Is(err, ErrNoManifestForLease):
		fallthrough
	case errors.Is(err, ErrNoServiceForLease):
		fallthrough
	case kubeErrors.IsNotFound(err):
		return httperror.NewHttpError(http.StatusNotFound, err)
	default:
		return httperror.NewHttpError(http.StatusInternalServerError, err)
	}
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
		strings.TrimSpace(msg) == "starting" ||
		strings.Contains(msg, "connection refused") || // fallback for syscall.ECONNREFUSED
		strings.Contains(msg, "connection reset") // fallback for syscall.ECONNRESET

}
