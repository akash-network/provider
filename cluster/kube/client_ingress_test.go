package kube

import (
	"testing"

	"github.com/stretchr/testify/require"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// TestKubeNginxIngressAnnotationsProxyOptions asserts the proxy_* http_options are
// rendered as community ingress-nginx annotations, that disabled buffering renders
// "off", and that the per-buffer size has no ingress annotation (NGF only).
func TestKubeNginxIngressAnnotationsProxyOptions(t *testing.T) {
	const root = "nginx.ingress.kubernetes.io"

	a := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferingDisable: true,
		ProxyBufferSize:       16384,
		ProxyBuffersNumber:    4,
		ProxyBuffersSize:      8192,
		ProxyBusyBuffersSize:  16384,
		ProxyConnectTimeout:   5000,
	})

	require.Equal(t, "off", a[root+"/proxy-buffering"])
	require.Equal(t, "16384", a[root+"/proxy-buffer-size"])
	require.Equal(t, "4", a[root+"/proxy-buffers-number"])
	require.Equal(t, "16384", a[root+"/proxy-busy-buffers-size"])
	require.Equal(t, "5", a[root+"/proxy-connect-timeout"])

	// Community ingress-nginx has no annotation for the per-buffer size.
	for k := range a {
		require.NotContains(t, k, "proxy-buffers-size")
	}
}

// TestKubeNginxIngressAnnotationsProxyOptionsUnset asserts none of the proxy_*
// annotations appear when the tenant did not set them. Buffering is on by default,
// so the unset directive must not emit a proxy-buffering annotation.
func TestKubeNginxIngressAnnotationsProxyOptionsUnset(t *testing.T) {
	const root = "nginx.ingress.kubernetes.io"

	a := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{})
	for _, k := range []string{"/proxy-buffering", "/proxy-buffer-size", "/proxy-buffers-number", "/proxy-busy-buffers-size", "/proxy-connect-timeout"} {
		_, ok := a[root+k]
		require.Falsef(t, ok, "unexpected annotation %s", root+k)
	}
}

// TestKubeNginxIngressAnnotationsProxyBuffersConsistency asserts a buffer set nginx
// would reject is dropped rather than emitted (an inconsistent set would freeze the
// shared ingress config on reload), while proxy-buffer-size stays since it is safe
// on its own.
func TestKubeNginxIngressAnnotationsProxyBuffersConsistency(t *testing.T) {
	const root = "nginx.ingress.kubernetes.io"

	// buffers-number 2 -> nginx default busy (2*buffer_size) exceeds (2-1)*buffer_size.
	a := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:    8192,
		ProxyBuffersNumber: 2,
	})
	require.Equal(t, "8192", a[root+"/proxy-buffer-size"], "buffer size is safe on its own")
	_, ok := a[root+"/proxy-buffers-number"]
	require.False(t, ok, "inconsistent buffers-number must be dropped")
	_, ok = a[root+"/proxy-busy-buffers-size"]
	require.False(t, ok, "inconsistent busy-buffers-size must be dropped")

	// explicit busy above (number-1)*buffer_size is dropped as a set.
	b := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:      8192,
		ProxyBuffersNumber:   4,
		ProxyBusyBuffersSize: 99999, // > (4-1)*8192
	})
	_, ok = b[root+"/proxy-busy-buffers-size"]
	require.False(t, ok, "busy over the max must be dropped")
	_, ok = b[root+"/proxy-buffers-number"]
	require.False(t, ok, "buffers-number dropped with the inconsistent set")

	// a consistent set is emitted.
	c := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:      8192,
		ProxyBuffersNumber:   4,
		ProxyBusyBuffersSize: 16384, // within [8192, (4-1)*8192]
	})
	require.Equal(t, "4", c[root+"/proxy-buffers-number"])
	require.Equal(t, "16384", c[root+"/proxy-busy-buffers-size"])
}

// TestKubeNginxIngressAnnotationsProxyBuffersPartial covers partial buffer sets: the
// helper fills the unset field from the effective default (ingress-nginx uses 4
// buffers) and emits the filled-in values, so a busy size is validated against the
// count ingress-nginx will actually apply - not nginx's raw 8 - and never renders a
// set the controller would reject.
func TestKubeNginxIngressAnnotationsProxyBuffersPartial(t *testing.T) {
	const root = "nginx.ingress.kubernetes.io"

	// busy set, number unset, busy too large for the default 4 buffers -> dropped.
	a := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:      8192,
		ProxyBusyBuffersSize: 32768, // > (4-1)*8192 = 24576
	})
	require.Equal(t, "8192", a[root+"/proxy-buffer-size"])
	_, ok := a[root+"/proxy-busy-buffers-size"]
	require.False(t, ok, "busy invalid against the default buffer count must be dropped")
	_, ok = a[root+"/proxy-buffers-number"]
	require.False(t, ok)

	// busy set, number unset, busy valid for 4 buffers -> emit the filled number too.
	b := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:      8192,
		ProxyBusyBuffersSize: 16384, // within [8192, (4-1)*8192]
	})
	require.Equal(t, "4", b[root+"/proxy-buffers-number"], "filled default count must be emitted")
	require.Equal(t, "16384", b[root+"/proxy-busy-buffers-size"])

	// number set, busy unset -> emit the filled busy (nginx default 2*buffer_size).
	c := kubeNginxIngressAnnotations(chostname.ConnectToDeploymentDirective{
		ProxyBufferSize:    8192,
		ProxyBuffersNumber: 6,
	})
	require.Equal(t, "6", c[root+"/proxy-buffers-number"])
	require.Equal(t, "16384", c[root+"/proxy-busy-buffers-size"], "filled default busy must be emitted")
}
