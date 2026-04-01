package kube

import (
	"context"

	mtypes "pkg.akt.dev/go/node/market/v1"
	metricsutils "pkg.akt.dev/node/util/metrics"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

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

func (c *client) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	routeName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)

	c.log.Info("creating HTTPRoute via Gateway API",
		"route-name", routeName,
		"namespace", ns,
		"gateway-name", c.gatewayName,
		"gateway-namespace", c.gatewayNamespace,
		"implementation", c.gatewayImpl.Name())

	warnings := gateway.ValidateDirective(c.gatewayImpl, directive)
	for _, warning := range warnings {
		c.log.Warn("gateway option not supported", "warning", warning)
	}

	config := gateway.HTTPRouteConfig{
		GatewayName:      c.gatewayName,
		GatewayNamespace: c.gatewayNamespace,
		Provider:         c.gatewayImpl,
	}

	return gateway.CreateOrUpdateHTTPRoute(ctx, c.dc, config, directive, httpRouteMetricsObserver{})
}

func (c *client) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)
	return gateway.DeleteHTTPRoute(ctx, c.dc, ns, hostname, allowMissing, httpRouteMetricsObserver{})
}

func (c *client) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	return gateway.ListHTTPRouteConnections(ctx, c.dc)
}
