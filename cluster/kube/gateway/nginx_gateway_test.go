package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

func testDirective() chostname.ConnectToDeploymentDirective {
	return chostname.ConnectToDeploymentDirective{
		Hostname:    "hello.localhost",
		ServiceName: "web",
		ServicePort: 80,
		MaxBodySize: 2097152,
		ReadTimeout: 60000,
		SendTimeout: 60000,
		NextTries:   3,
		NextCases:   []string{"off"},
	}
}

// TestHTTPOptionsSnippet asserts the directive renders to the expected nginx
// directives (read/send timeouts preserved in milliseconds).
func TestHTTPOptionsSnippet(t *testing.T) {
	snip := httpOptionsSnippet(testDirective())

	for _, want := range []string{
		"client_max_body_size 2097152;",
		"proxy_read_timeout 60000ms;",
		"proxy_send_timeout 60000ms;",
		"proxy_next_upstream_tries 3;",
		"proxy_next_upstream off;",
	} {
		require.Containsf(t, snip, want, "snippet missing %q", want)
	}
}

// TestHTTPOptionsSnippetEmpty asserts an unset directive produces no snippet, so
// no SnippetsFilter and no filter are emitted.
func TestHTTPOptionsSnippetEmpty(t *testing.T) {
	require.Empty(t, httpOptionsSnippet(chostname.ConnectToDeploymentDirective{}))
}

// TestHTTPOptionsSnippetProxyBuffering asserts disabled buffering renders "off" and
// that buffering is omitted otherwise (nginx default is on).
func TestHTTPOptionsSnippetProxyBuffering(t *testing.T) {
	require.Contains(t, httpOptionsSnippet(chostname.ConnectToDeploymentDirective{ProxyBufferingDisable: true}), "proxy_buffering off;")
	require.NotContains(t, httpOptionsSnippet(chostname.ConnectToDeploymentDirective{}), "proxy_buffering")
	require.NotContains(t, httpOptionsSnippet(testDirective()), "proxy_buffering")
}

// TestHTTPOptionsSnippetProxyConnectTimeout asserts the ms value is rendered as
// whole seconds.
func TestHTTPOptionsSnippetProxyConnectTimeout(t *testing.T) {
	require.Contains(t, httpOptionsSnippet(chostname.ConnectToDeploymentDirective{ProxyConnectTimeout: 5000}), "proxy_connect_timeout 5s;")
}

// TestProxyBufferLines asserts the buffer directives are only emitted as a
// complete, nginx-consistent group, and an inconsistent tenant set is dropped so
// it can never break the shared gateway config.
func TestProxyBufferLines(t *testing.T) {
	d := func(b, n, s, busy uint32) chostname.ConnectToDeploymentDirective {
		return chostname.ConnectToDeploymentDirective{
			ProxyBufferSize:      b,
			ProxyBuffersNumber:   n,
			ProxyBuffersSize:     s,
			ProxyBusyBuffersSize: busy,
		}
	}
	cases := []struct {
		name string
		in   chostname.ConnectToDeploymentDirective
		want []string
	}{
		{"none", d(0, 0, 0, 0), nil},
		{"buffer_size alone completes the pool", d(16384, 0, 0, 0), []string{"proxy_buffer_size 16384;", "proxy_buffers 8 16384;", "proxy_busy_buffers_size 16384;"}},
		{"full valid trio", d(8192, 4, 8192, 16384), []string{"proxy_buffer_size 8192;", "proxy_buffers 4 8192;", "proxy_busy_buffers_size 16384;"}},
		{"buffers only", d(0, 4, 8192, 0), []string{"proxy_buffer_size 8192;", "proxy_buffers 4 8192;", "proxy_busy_buffers_size 8192;"}},
		{"busy too large dropped", d(8192, 4, 8192, 99999), nil},
		{"fewer than two buffers dropped", d(0, 1, 8192, 0), nil},
		{"buffers number without size dropped", d(0, 4, 0, 0), nil},
		{"busy alone dropped", d(0, 0, 0, 16384), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, proxyBufferLines(tc.in))
		})
	}
}

// TestHTTPOptionsSnippetNextCasesValidation asserts next_cases values outside the
// closed proxy_next_upstream token set are dropped, so tenant input cannot inject
// nginx directives, while valid tokens and status codes still render.
func TestHTTPOptionsSnippetNextCasesValidation(t *testing.T) {
	d := chostname.ConnectToDeploymentDirective{
		NextCases: []string{"error", "500", "http_503", "off; } location /x { proxy_pass http://evil;", "bogus", "400", ""},
	}
	snip := httpOptionsSnippet(d)
	require.Equal(t, "proxy_next_upstream error http_500 http_503;\n", snip)
	require.NotContains(t, snip, "evil")
	require.NotContains(t, snip, "}")
}

// TestHTTPOptionsSnippetNextCasesAllInvalid asserts the directive is omitted
// entirely when no next_cases value survives validation.
func TestHTTPOptionsSnippetNextCasesAllInvalid(t *testing.T) {
	d := chostname.ConnectToDeploymentDirective{NextCases: []string{"; drop;", "nonsense"}}
	require.Empty(t, httpOptionsSnippet(d))
}

// TestBuildRouteExtensionsSnippetsFilter asserts BuildRouteExtensions emits a
// SnippetsFilter named after the route carrying the snippet at the location
// context, and nil when no http_options are set.
func TestBuildRouteExtensionsSnippetsFilter(t *testing.T) {
	gw := NewNginxGateway(log.NewNopLogger())

	require.Nil(t, gw.BuildRouteExtensions("ns1", "hello.localhost", chostname.ConnectToDeploymentDirective{}))

	exts := gw.BuildRouteExtensions("ns1", "hello.localhost", testDirective())
	require.Len(t, exts, 1)
	sf := exts[0]
	require.Equal(t, "gateway.nginx.org/v1alpha1", sf.GetAPIVersion())
	require.Equal(t, "SnippetsFilter", sf.GetKind())
	require.Equal(t, "hello.localhost", sf.GetName())
	require.Equal(t, "ns1", sf.GetNamespace())

	snippets, found, err := unstructured.NestedSlice(sf.Object, "spec", "snippets")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, snippets, 1)
	first, ok := snippets[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "http.server.location", first["context"])
	require.Contains(t, first["value"], "client_max_body_size 2097152;")
}

// TestBuildHTTPRouteSpecExtensionRef asserts the rule gains an ExtensionRef filter
// to the route's SnippetsFilter when http_options are set, and none when unset.
func TestBuildHTTPRouteSpecExtensionRef(t *testing.T) {
	gw := NewNginxGateway(log.NewNopLogger())

	bare := gw.BuildHTTPRouteSpec("gw", "gwns", "hello.localhost", "web", 80, nil)
	require.Empty(t, bare.Rules[0].Filters)

	exts := gw.BuildRouteExtensions("ns1", "hello.localhost", testDirective())
	spec := gw.BuildHTTPRouteSpec("gw", "gwns", "hello.localhost", "web", 80, exts)
	require.Len(t, spec.Rules[0].Filters, 1)
	f := spec.Rules[0].Filters[0]
	require.Equal(t, gatewayv1.HTTPRouteFilterExtensionRef, f.Type)
	require.NotNil(t, f.ExtensionRef)
	require.Equal(t, gatewayv1.Kind("SnippetsFilter"), f.ExtensionRef.Kind)
	require.Equal(t, gatewayv1.Group("gateway.nginx.org"), f.ExtensionRef.Group)
	require.Equal(t, gatewayv1.ObjectName("hello.localhost"), f.ExtensionRef.Name)
}
