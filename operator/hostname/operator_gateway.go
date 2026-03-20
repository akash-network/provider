package hostname

import (
	"context"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

func (op *hostnameOperator) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	warnings := gateway.ValidateDirective(op.gatewayImpl, directive)
	for _, warning := range warnings {
		op.log.Warn("gateway option not supported", "warning", warning)
	}

	config := gateway.HTTPRouteConfig{
		GatewayName:      op.ingressConfig.GatewayName,
		GatewayNamespace: op.ingressConfig.GatewayNamespace,
		Implementation:   op.gatewayImpl,
	}

	return gateway.CreateOrUpdateHTTPRoute(ctx, op.dc, config, directive, gateway.NoopHTTPRouteObserver{})
}

func (op *hostnameOperator) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)
	return gateway.DeleteHTTPRoute(ctx, op.dc, ns, hostname, allowMissing, gateway.NoopHTTPRouteObserver{})
}

func (op *hostnameOperator) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	return gateway.ListHTTPRouteConnections(ctx, op.dc)
}
