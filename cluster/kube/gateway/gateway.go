package gateway

import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// Implementation defines the interface for Gateway API implementations.
// Each implementation (NGINX, Istio, Kong, etc.) provides its own strategy for:
// - Converting directive options to HTTPRoute annotations/filters
// - Building HTTPRoute rules with implementation-specific enhancements
// - Handling unsupported features with warnings
type Implementation interface {
	// Name returns the implementation identifier (e.g., "nginx", "istio", "kong")
	Name() string

	// BuildAnnotations converts directive options to implementation-specific annotations.
	// These annotations are applied to the HTTPRoute metadata and control behavior
	// such as timeouts, body size limits, and retry policies.
	BuildAnnotations(directive chostname.ConnectToDeploymentDirective) map[string]string

	// BuildHTTPRouteSpec builds the HTTPRoute spec with implementation-specific features.
	// This includes standard Gateway API features (hostnames, routes, backends) as well as
	// implementation-specific filters and configurations.
	BuildHTTPRouteSpec(
		gatewayName string,
		gatewayNamespace string,
		hostname string,
		serviceName string,
		servicePort int32,
		directive chostname.ConnectToDeploymentDirective,
	) gatewayv1.HTTPRouteSpec

	// ValidateOptions checks if directive options are supported by this implementation
	// and returns warnings for unsupported features. This allows graceful degradation
	// where unsupported options are logged but don't cause failures.
	ValidateOptions(directive chostname.ConnectToDeploymentDirective) []string
}

