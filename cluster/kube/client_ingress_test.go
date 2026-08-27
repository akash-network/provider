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
		ProxyBufferSize:        16384,
		ProxyBuffersNumber:     4,
		ProxyBuffersSize:       8192,
		ProxyBusyBuffersSize:   16384,
		ProxyConnectTimeout:    5000,
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
