package gateway

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"cosmossdk.io/log"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// nginxGateway implements the Gateway API interface for NGINX Gateway Fabric.
// It uses a hybrid approach:
// - Standard Gateway API features (HTTPRoute timeouts) where available
// - NGINX-specific annotations for features not in the standard (body size, retries)
type nginxGateway struct {
	log log.Logger
}

// NewNginxGateway creates a new NGINX Gateway Fabric implementation.
func NewNginxGateway(logger log.Logger) Implementation {
	return &nginxGateway{log: logger}
}

// Name returns the implementation identifier.
func (n *nginxGateway) Name() string {
	return "nginx"
}

// BuildHTTPRouteSpec builds the HTTPRoute spec using standard Gateway API features.
// Timeouts are configured using the standard HTTPRoute.Spec.Rules[].Timeouts field
// rather than NGINX-specific annotations.
func (n *nginxGateway) BuildHTTPRouteSpec(
	gatewayName string,
	gatewayNamespace string,
	hostname string,
	serviceName string,
	servicePort int32,
	directive chostname.ConnectToDeploymentDirective,
) gatewayv1.HTTPRouteSpec {
	// Build parent reference to the Gateway
	parentRefs := []gatewayv1.ParentReference{
		{
			Group:     (*gatewayv1.Group)(&gatewayv1.GroupVersion.Group),
			Kind:      (*gatewayv1.Kind)(strPtr("Gateway")),
			Namespace: (*gatewayv1.Namespace)(&gatewayNamespace),
			Name:      gatewayv1.ObjectName(gatewayName),
		},
	}

	// Configure standard Gateway API timeouts
	// ReadTimeout maps to Request timeout (client to gateway)
	// SendTimeout maps to BackendRequest timeout (gateway to backend)
	// Gateway API Duration type is a string formatted as a Go duration (e.g., "60s", "1m")
	requestTimeout := gatewayv1.Duration((time.Duration(directive.ReadTimeout) * time.Millisecond).String())
	backendRequestTimeout := gatewayv1.Duration((time.Duration(directive.SendTimeout) * time.Millisecond).String())
	timeouts := &gatewayv1.HTTPRouteTimeouts{
		Request:        &requestTimeout,
		BackendRequest: &backendRequestTimeout,
	}

	// Build HTTP route rules
	pathType := gatewayv1.PathMatchPathPrefix
	backendPort := gatewayv1.PortNumber(servicePort)

	rules := []gatewayv1.HTTPRouteRule{
		{
			Matches: []gatewayv1.HTTPRouteMatch{
				{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  &pathType,
						Value: strPtr("/"),
					},
				},
			},
			BackendRefs: []gatewayv1.HTTPBackendRef{
				{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(serviceName),
							Port: &backendPort,
						},
					},
				},
			},
			Timeouts: timeouts,
		},
	}

	// Return the complete HTTPRoute spec
	return gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{
			ParentRefs: parentRefs,
		},
		Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
		Rules:     rules,
	}
}

// BuildAnnotations creates NGINX-specific annotations for features not available
// in the standard Gateway API v1 spec.
//
// Annotations used:
// - nginx.org/client-max-body-size: Request body size limit (no standard equivalent)
// - nginx.org/proxy-next-upstream-timeout: Retry timeout (retry policies experimental in Gateway API)
// - nginx.org/proxy-next-upstream-tries: Maximum retry attempts
// - nginx.org/proxy-next-upstream: Conditions for retrying requests
//
// Note: Timeout annotations (proxy-connect-timeout, proxy-read-timeout, proxy-send-timeout)
// are NOT used because they're handled by standard HTTPRoute.Spec.Rules[].Timeouts
func (n *nginxGateway) BuildAnnotations(directive chostname.ConnectToDeploymentDirective) map[string]string {
	annotations := make(map[string]string)

	// Client max body size - no standard Gateway API equivalent
	// This controls the maximum size of the client request body
	annotations["nginx.org/client-max-body-size"] = strconv.Itoa(int(directive.MaxBodySize))

	// Retry/next upstream configuration - not in Gateway API v1 standard
	// Gateway API has experimental retry policies, but they're not widely supported yet
	nextTimeout := 0
	if directive.NextTimeout > 0 {
		nextTimeout = int(math.Ceil(float64(directive.NextTimeout) / 1000.0))
	}

	if nextTimeout > 0 {
		annotations["nginx.org/proxy-next-upstream-timeout"] = fmt.Sprintf("%ds", nextTimeout)
	}

	annotations["nginx.org/proxy-next-upstream-tries"] = strconv.Itoa(int(directive.NextTries))

	// Build next-upstream cases (error, timeout, http_500, etc.)
	if len(directive.NextCases) > 0 {
		strBuilder := strings.Builder{}
		for i, v := range directive.NextCases {
			first := string(v[0])
			isHTTPCode := strings.ContainsAny(first, "12345")

			if isHTTPCode {
				strBuilder.WriteString("http_")
			}
			strBuilder.WriteString(v)

			if i != len(directive.NextCases)-1 {
				strBuilder.WriteRune(' ')
			}
		}
		annotations["nginx.org/proxy-next-upstream"] = strBuilder.String()
	}

	return annotations
}

// ValidateOptions checks if all directive options are supported by NGINX Gateway Fabric.
// Currently, all Akash directive options are supported by NGINX, so this returns an empty slice.
// In the future, if new directive options are added that NGINX doesn't support, they should
// be detected here and returned as warnings.
func (n *nginxGateway) ValidateOptions(directive chostname.ConnectToDeploymentDirective) []string {
	var warnings []string

	// All directive options are currently supported by NGINX Gateway Fabric:
	// - ReadTimeout -> HTTPRoute.Spec.Rules[].Timeouts.Request
	// - SendTimeout -> HTTPRoute.Spec.Rules[].Timeouts.BackendRequest
	// - MaxBodySize -> nginx.org/client-max-body-size annotation
	// - NextTimeout -> nginx.org/proxy-next-upstream-timeout annotation
	// - NextTries -> nginx.org/proxy-next-upstream-tries annotation
	// - NextCases -> nginx.org/proxy-next-upstream annotation

	return warnings
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}
