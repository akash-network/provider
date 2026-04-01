package gateway

import (
	"fmt"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// GatewayProvider defines the interface for Gateway API providers.
// Each provider (NGINX, Istio, Kong, etc.) provides its own strategy for:
// - Converting directive options to HTTPRoute annotations/filters
// - Building HTTPRoute rules with implementation-specific enhancements
// - Handling unsupported features with warnings
type GatewayProvider interface {
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

	// SupportedDirectives returns the list of directive option names that this
	// implementation supports. Used by ValidateDirective to warn about unsupported options.
	SupportedDirectives() []string
}

// ValidateDirective checks if all set directive options are supported by the implementation.
// Returns warnings for any options that are set but not supported.
//
// NOTE: When adding new option fields to ConnectToDeploymentDirective, you must add
// a corresponding check here. A test using reflection verifies all fields are covered.
func ValidateDirective(impl GatewayProvider, directive chostname.ConnectToDeploymentDirective) []string {
	supported := make(map[string]bool)
	for _, name := range impl.SupportedDirectives() {
		supported[name] = true
	}

	var warnings []string

	if directive.ReadTimeout != 0 && !supported["ReadTimeout"] {
		warnings = append(warnings, fmt.Sprintf("ReadTimeout is not supported by %s", impl.Name()))
	}
	if directive.SendTimeout != 0 && !supported["SendTimeout"] {
		warnings = append(warnings, fmt.Sprintf("SendTimeout is not supported by %s", impl.Name()))
	}
	if directive.MaxBodySize != 0 && !supported["MaxBodySize"] {
		warnings = append(warnings, fmt.Sprintf("MaxBodySize is not supported by %s", impl.Name()))
	}
	if directive.NextTimeout != 0 && !supported["NextTimeout"] {
		warnings = append(warnings, fmt.Sprintf("NextTimeout is not supported by %s", impl.Name()))
	}
	if directive.NextTries != 0 && !supported["NextTries"] {
		warnings = append(warnings, fmt.Sprintf("NextTries is not supported by %s", impl.Name()))
	}
	if len(directive.NextCases) > 0 && !supported["NextCases"] {
		warnings = append(warnings, fmt.Sprintf("NextCases is not supported by %s", impl.Name()))
	}

	return warnings
}
