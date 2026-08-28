package gateway

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"cosmossdk.io/log"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

const (
	snippetsFilterAPIVersion = "gateway.nginx.org/v1alpha1"
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

// BuildHTTPRouteSpec builds the HTTPRoute spec. The rule gains one ExtensionRef
// filter per route extension (built by BuildRouteExtensions), derived from the
// extension objects themselves so NGF injects each SnippetsFilter into the
// location for this route.
func (n *nginxGateway) BuildHTTPRouteSpec(
	gatewayName string,
	gatewayNamespace string,
	hostname string,
	serviceName string,
	servicePort int32,
	extensions []*unstructured.Unstructured,
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
	for _, ext := range extensions {
		gvk := ext.GroupVersionKind()
		filters = append(filters, gatewayv1.HTTPRouteFilter{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: gatewayv1.Group(gvk.Group),
				Kind:  gatewayv1.Kind(gvk.Kind),
				Name:  gatewayv1.ObjectName(ext.GetName()),
			},
		})
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
	snippet := httpOptionsSnippet(directive)
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

// httpOptionsSnippet renders the directive's http_options as nginx directives for
// the location context. Returns "" when nothing is set. Read, send, and
// next-upstream timeouts are emitted in milliseconds to preserve the manifest's
// precision (nginx accepts an ms suffix); the connect timeout is rounded up to
// whole seconds. The proxy buffer directives are emitted only as a complete,
// nginx-consistent group (see proxyBufferLines); an inconsistent tenant set is
// dropped so it can never break the shared gateway's configuration.
func httpOptionsSnippet(d chostname.ConnectToDeploymentDirective) string {
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
	if d.ProxyConnectTimeout > 0 {
		line("proxy_connect_timeout %ds;", msToSec(d.ProxyConnectTimeout))
	}
	// Buffering is on by default, so only the explicit "off" needs a directive.
	if d.ProxyBufferingDisable {
		line("proxy_buffering off;")
	}
	for _, l := range proxyBufferLines(d) {
		line("%s", l)
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

// proxyBufferLines renders proxy_buffer_size, proxy_buffers, and
// proxy_busy_buffers_size as one complete, nginx-consistent group, or nothing.
// These directives are interdependent: nginx rejects the whole config on reload
// unless proxy_busy_buffers_size is within [max(proxy_buffer_size, one buffer),
// (buffers-1)*buffer] and there are at least 2 buffers. A rejected reload freezes
// config for every tenant on the shared gateway, so the provider must never emit a
// combination it cannot prove valid. It fills any value the tenant left unset from
// what they did set (so the emitted group never depends on nginx's platform default
// buffer size) and drops the entire group if the explicit values cannot form a
// valid set. All sizes are bytes.
func proxyBufferLines(d chostname.ConnectToDeploymentDirective) []string {
	bufferSize := d.ProxyBufferSize
	poolNum := d.ProxyBuffersNumber
	poolSize := d.ProxyBuffersSize
	busy := d.ProxyBusyBuffersSize

	if (poolNum == 0) != (poolSize == 0) {
		return nil
	}
	if bufferSize == 0 && poolNum == 0 && busy == 0 {
		return nil
	}

	if bufferSize == 0 {
		bufferSize = poolSize
	}
	if poolSize == 0 {
		poolSize = bufferSize
	}
	if poolNum == 0 {
		poolNum = 8
	}
	if bufferSize == 0 || poolSize == 0 || poolNum < 2 {
		return nil
	}

	minBusy := bufferSize
	if poolSize > minBusy {
		minBusy = poolSize
	}
	maxBusy := (poolNum - 1) * poolSize
	if busy == 0 {
		busy = minBusy
	}
	if busy < minBusy || busy > maxBusy {
		return nil
	}

	return []string{
		fmt.Sprintf("proxy_buffer_size %d;", bufferSize),
		fmt.Sprintf("proxy_buffers %d %d;", poolNum, poolSize),
		fmt.Sprintf("proxy_busy_buffers_size %d;", busy),
	}
}

func msToSec(ms uint32) int {
	return int(math.Ceil(float64(ms) / 1000.0))
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
