package rest

import (
	"errors"
	"net/http"

	kubeErrors "k8s.io/apimachinery/pkg/api/errors"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	"github.com/akash-network/provider/pkg/httperror"
)

func clusterErrorToHTTP(err error) error {
	switch {
	case err == nil:
		return nil
	case kubeclienterrors.IsClusterUnavailable(err):
		return httperror.NewHttpError(http.StatusServiceUnavailable, err)
	case errors.Is(err, kubeclienterrors.ErrNoDeploymentForLease):
		fallthrough
	case errors.Is(err, kubeclienterrors.ErrLeaseNotFound):
		fallthrough
	case errors.Is(err, kubeclienterrors.ErrNoManifestForLease):
		fallthrough
	case errors.Is(err, kubeclienterrors.ErrNoServiceForLease):
		fallthrough
	case kubeErrors.IsNotFound(err):
		return httperror.NewHttpError(http.StatusNotFound, err)
	default:
		return httperror.NewHttpError(http.StatusInternalServerError, err)
	}
}
