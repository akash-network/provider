package gateway

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"cosmossdk.io/log"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
)

const (
	snippetsFilterAPIVersion = "gateway.nginx.org/v1alpha1"
	snippetsFilterGroup      = "gateway.nginx.org"
	snippetsFilterKind       = "SnippetsFilter"
)

// nginxGateway implements the Gateway API interface for NGINX Gateway Fabric.
// NGF does not consume nginx.org/* annotations (those belong to the NGINX Ingress
// Controller). The http_options are applied as a per-route SnippetsFilter carrying
// raw nginx directives. NGF must run with --snippets for this to take effect.
type nginxGateway struct {
	log log.Logger
}

// NewNginxGateway creates a new NGINX Gateway Fabric provider.
func NewNginxGateway(logger log.Logger) GatewayProvider {
	return &nginxGateway{log: logger}
}

// Name returns the implementation identifier.
func (n *nginxGateway) Name() string {
	return "nginx"
}

// BuildHTTPRouteSpec builds the HTTPRoute spec. When the directive carries any
// http_options, the rule references a per-route SnippetsFilter (built by
// BuildRouteExtensions) via an ExtensionRef filter so NGF injects the directives
// into the location for this route.
func (n *nginxGateway) BuildHTTPRouteSpec(
	gatewayName string,
	gatewayNamespace string,
	hostname string,
	serviceName string,
	servicePort int32,
	directive chostname.ConnectToDeploymentDirective,
) gatewayv1.HTTPRouteSpec {
	parentRefs := []gatewayv1.ParentReference{
		{
			Group:     (*gatewayv1.Group)(&gatewayv1.GroupVersion.Group),
			Kind:      (*gatewayv1.Kind)(strPtr("Gateway")),
			Namespace: (*gatewayv1.Namespace)(&gatewayNamespace),
			Name:      gatewayv1.ObjectName(gatewayName),
		},
	}

	pathType := gatewayv1.PathMatchPathPrefix
	backendPort := gatewayv1.PortNumber(servicePort)

	var filters []gatewayv1.HTTPRouteFilter
	if httpOptionsSnippet(directive, proxyBufferSize()) != "" {
		filters = []gatewayv1.HTTPRouteFilter{
			{
				Type: gatewayv1.HTTPRouteFilterExtensionRef,
				ExtensionRef: &gatewayv1.LocalObjectReference{
					Group: gatewayv1.Group(snippetsFilterGroup),
					Kind:  gatewayv1.Kind(snippetsFilterKind),
					Name:  gatewayv1.ObjectName(hostname),
				},
			},
		}
	}

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
			Filters: filters,
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
		},
	}

	return gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{
			ParentRefs: parentRefs,
		},
		Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(hostname)},
		Rules:     rules,
	}
}

func (n *nginxGateway) BuildAnnotations(_ chostname.ConnectToDeploymentDirective) map[string]string {
	return nil
}

// BuildRouteExtensions returns the auxiliary CRD objects to apply for a route. For
// NGF that is a SnippetsFilter named after the route, carrying the http_options as
// raw nginx directives at the location context. Returns nil when nothing is set.
func (n *nginxGateway) BuildRouteExtensions(namespace, routeName string, directive chostname.ConnectToDeploymentDirective) []*unstructured.Unstructured {
	snippet := httpOptionsSnippet(directive, proxyBufferSize())
	if snippet == "" {
		return nil
	}

	sf := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": snippetsFilterAPIVersion,
		"kind":       snippetsFilterKind,
		"metadata": map[string]interface{}{
			"name":      routeName,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"snippets": []interface{}{
				map[string]interface{}{
					"context": "http.server.location",
					"value":   snippet,
				},
			},
		},
	}}

	return []*unstructured.Unstructured{sf}
}

// proxyBufferSize returns the provider-wide proxy buffer size (nginx size string,
// e.g. "16k") from the --proxy-buffer-size flag, preserved from the previous
// annotation-based plumbing.
func proxyBufferSize() string {
	return viper.GetString(providerflags.FlagProxyBufferSize)
}

// httpOptionsSnippet renders the directive's http_options (plus the provider-wide
// proxyBufferSize) as nginx directives for the location context. Returns "" when
// nothing is set. Timeouts are emitted in milliseconds to preserve the manifest's
// precision (nginx accepts an ms suffix).
func httpOptionsSnippet(d chostname.ConnectToDeploymentDirective, proxyBufferSize string) string {
	var b strings.Builder
	line := func(format string, args ...interface{}) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	if d.MaxBodySize > 0 {
		line("client_max_body_size %d;", d.MaxBodySize)
	}
	if d.ReadTimeout > 0 {
		line("proxy_read_timeout %dms;", d.ReadTimeout)
	}
	if d.SendTimeout > 0 {
		line("proxy_send_timeout %dms;", d.SendTimeout)
	}
	if proxyBufferSize != "" {
		// proxy_buffer_size alone makes nginx's default proxy_busy_buffers_size
		// (2x) exceed the default proxy_buffers pool, so the config is rejected on
		// reload. Pair it with a matching proxy_buffers so it is valid for any size.
		line("proxy_buffer_size %s;", proxyBufferSize)
		line("proxy_buffers 8 %s;", proxyBufferSize)
	}
	if d.NextTries > 0 {
		line("proxy_next_upstream_tries %d;", d.NextTries)
	}
	if d.NextTimeout > 0 {
		line("proxy_next_upstream_timeout %dms;", d.NextTimeout)
	}
	if len(d.NextCases) > 0 {
		cases := make([]string, 0, len(d.NextCases))
		for _, c := range d.NextCases {
			if tok, ok := normalizeNextCase(c); ok {
				cases = append(cases, tok)
			}
		}
		if len(cases) > 0 {
			line("proxy_next_upstream %s;", strings.Join(cases, " "))
		}
	}

	return b.String()
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}

// validProxyNextUpstreamTokens is the closed set of tokens nginx accepts for
// proxy_next_upstream. next_cases comes from the tenant SDL and is rendered into
// a raw nginx snippet, so each value is validated against this set (bare status
// codes normalized to the http_ form first) before it reaches the config. Values
// outside the set are dropped, so a semicolon, newline, or brace cannot inject
// directives or break the shared nginx configuration.
var validProxyNextUpstreamTokens = map[string]struct{}{
	"error":          {},
	"timeout":        {},
	"invalid_header": {},
	"http_500":       {},
	"http_502":       {},
	"http_503":       {},
	"http_504":       {},
	"http_403":       {},
	"http_404":       {},
	"http_429":       {},
	"non_idempotent": {},
	"off":            {},
}

func normalizeNextCase(c string) (string, bool) {
	if _, err := strconv.Atoi(c); err == nil {
		c = "http_" + c
	}
	_, ok := validProxyNextUpstreamTokens[c]
	return c, ok
}
