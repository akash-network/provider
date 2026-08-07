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

// TestHTTPOptionsSnippet asserts the directive plus the provider-wide buffer size
// render to the expected nginx directives (ms rounded to whole seconds).
func TestHTTPOptionsSnippet(t *testing.T) {
	snip := httpOptionsSnippet(testDirective(), "16k")

	for _, want := range []string{
		"client_max_body_size 2097152;",
		"proxy_read_timeout 60000ms;",
		"proxy_send_timeout 60000ms;",
		"proxy_buffer_size 16k;",
		"proxy_buffers 8 16k;",
		"proxy_next_upstream_tries 3;",
		"proxy_next_upstream off;",
	} {
		require.Containsf(t, snip, want, "snippet missing %q", want)
	}
}

// TestHTTPOptionsSnippetEmpty asserts an unset directive with no buffer size
// produces no snippet, so no SnippetsFilter and no filter are emitted.
func TestHTTPOptionsSnippetEmpty(t *testing.T) {
	require.Empty(t, httpOptionsSnippet(chostname.ConnectToDeploymentDirective{}, ""))
}

// TestHTTPOptionsSnippetBufferOnly asserts the provider-wide buffer size alone
// still yields a snippet (preserving the old global proxy-buffer-size behaviour).
func TestHTTPOptionsSnippetBufferOnly(t *testing.T) {
	require.Equal(t, "proxy_buffer_size 16k;\nproxy_buffers 8 16k;\n", httpOptionsSnippet(chostname.ConnectToDeploymentDirective{}, "16k"))
}

// TestHTTPOptionsSnippetNextCasesValidation asserts next_cases values outside the
// closed proxy_next_upstream token set are dropped, so tenant input cannot inject
// nginx directives, while valid tokens and status codes still render.
func TestHTTPOptionsSnippetNextCasesValidation(t *testing.T) {
	d := chostname.ConnectToDeploymentDirective{
		NextCases: []string{"error", "500", "http_503", "off; } location /x { proxy_pass http://evil;", "bogus", "400", ""},
	}
	snip := httpOptionsSnippet(d, "")
	require.Equal(t, "proxy_next_upstream error http_500 http_503;\n", snip)
	require.NotContains(t, snip, "evil")
	require.NotContains(t, snip, "}")
}

// TestHTTPOptionsSnippetNextCasesAllInvalid asserts the directive is omitted
// entirely when no next_cases value survives validation.
func TestHTTPOptionsSnippetNextCasesAllInvalid(t *testing.T) {
	d := chostname.ConnectToDeploymentDirective{NextCases: []string{"; drop;", "nonsense"}}
	require.Empty(t, httpOptionsSnippet(d, ""))
}

// TestBuildRouteExtensionsSnippetsFilter asserts BuildRouteExtensions emits a
// SnippetsFilter named after the route carrying the snippet at the location
// context, and nil when nothing is set (viper is unset in tests, so no buffer).
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

	bare := gw.BuildHTTPRouteSpec("gw", "gwns", "hello.localhost", "web", 80, chostname.ConnectToDeploymentDirective{})
	require.Empty(t, bare.Rules[0].Filters)

	spec := gw.BuildHTTPRouteSpec("gw", "gwns", "hello.localhost", "web", 80, testDirective())
	require.Len(t, spec.Rules[0].Filters, 1)
	f := spec.Rules[0].Filters[0]
	require.Equal(t, gatewayv1.HTTPRouteFilterExtensionRef, f.Type)
	require.NotNil(t, f.ExtensionRef)
	require.Equal(t, gatewayv1.Kind("SnippetsFilter"), f.ExtensionRef.Kind)
	require.Equal(t, gatewayv1.Group("gateway.nginx.org"), f.ExtensionRef.Group)
	require.Equal(t, gatewayv1.ObjectName("hello.localhost"), f.ExtensionRef.Name)
}
