package hostname

import (
	"context"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

func (op *hostnameOperator) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	config := gateway.HTTPRouteConfig{
		GatewayName:      op.gatewayName,
		GatewayNamespace: op.gatewayNamespace,
		Provider:         op.gatewayImpl,
	}

	return gateway.CreateOrUpdateHTTPRoute(ctx, op.dc, config, directive, gateway.NoopHTTPRouteObserver{})
}

func (op *hostnameOperator) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)
	return gateway.DeleteHTTPRoute(ctx, op.dc, ns, hostname, allowMissing, gateway.NoopHTTPRouteObserver{})
}

// hostnameRouteExists reports whether the hostname's HTTPRoute exists in the
// lease's namespace.
func (op *hostnameOperator) hostnameRouteExists(ctx context.Context, hostname string, leaseID mtypes.LeaseID) (bool, error) {
	_, err := op.dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(leaseID)).Get(ctx, hostname, metav1.GetOptions{})
	switch {
	case err == nil:
		return true, nil
	case kerrors.IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

func (op *hostnameOperator) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	return gateway.ListHTTPRouteConnections(ctx, op.dc)
}
