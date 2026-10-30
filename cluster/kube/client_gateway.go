package kube

import (
	"context"
	"errors"

	mtypes "pkg.akt.dev/go/node/market/v1"
	metricsutils "pkg.akt.dev/node/util/metrics"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// errGatewayProviderUnavailable is returned when the Gateway API implementation
// could not be resolved at startup. The hostname operator, not provider-services,
// renders tenant routes, so the kube client never fails construction over it.
var errGatewayProviderUnavailable = errors.New("kube: gateway provider unavailable")

// httpRouteMetricsObserver implements gateway.HTTPRouteObserver for metrics tracking.
type httpRouteMetricsObserver struct{}

func (httpRouteMetricsObserver) OnCreate(err error) {
	metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-create", err)
}

func (httpRouteMetricsObserver) OnUpdate(err error) {
	metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-update", err)
}

func (httpRouteMetricsObserver) OnDelete(err error) {
	metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-delete", err)
}

var _ gateway.HTTPRouteObserver = httpRouteMetricsObserver{}

func (c *client) ConnectHostnameToDeployment(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	if c.gatewayImpl == nil {
		return errGatewayProviderUnavailable
	}

	log := c.log.With("lid", directive.LeaseID)

	routeName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)

	log.Info("creating HTTPRoute via Gateway API",
		"route_name", routeName,
		"ns", ns,
		"gateway_name", c.gatewayName,
		"gateway_namespace", c.gatewayNamespace,
		"implementation", c.gatewayImpl.Name())

	config := gateway.HTTPRouteConfig{
		GatewayName:      c.gatewayName,
		GatewayNamespace: c.gatewayNamespace,
		Provider:         c.gatewayImpl,
	}

	return gateway.CreateOrUpdateHTTPRoute(ctx, c.dc, config, directive, httpRouteMetricsObserver{})
}

func (c *client) RemoveHostnameFromDeployment(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)
	return gateway.DeleteHTTPRoute(ctx, c.dc, ns, hostname, allowMissing, httpRouteMetricsObserver{})
}

func (c *client) GetHostnameDeploymentConnections(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	return gateway.ListHTTPRouteConnections(ctx, c.dc)
}
