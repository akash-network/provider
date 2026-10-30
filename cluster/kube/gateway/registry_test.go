package gateway

import (
	"context"
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// seedGateway creates akash-gateway/akash-gateway on a GatewayClass owned by the
// given controller.
func seedGateway(t *testing.T, controller string) *dynamicfake.FakeDynamicClient {
	t.Helper()
	dc := newFakeDC()
	ctx := context.Background()

	class := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "GatewayClass",
		"metadata":   map[string]interface{}{"name": "cls"},
		"spec":       map[string]interface{}{"controllerName": controller},
	}}
	_, err := dc.Resource(GatewayClassGVR).Create(ctx, class, metav1.CreateOptions{})
	require.NoError(t, err)

	gw := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]interface{}{"name": "akash-gateway", "namespace": "akash-gateway"},
		"spec":       map[string]interface{}{"gatewayClassName": "cls"},
	}}
	_, err = dc.Resource(GatewayGVR).Namespace("akash-gateway").Create(ctx, gw, metav1.CreateOptions{})
	require.NoError(t, err)

	return dc
}

// TestResolveProviderAuto asserts auto detection maps the GatewayClass controller
// to its provider, empty means auto, and an explicit name overrides detection.
func TestResolveProviderAuto(t *testing.T) {
	ctx := context.Background()
	logger := log.NewNopLogger()

	for _, tc := range []struct {
		controller string
		want       string
	}{
		{nginxControllerName, "nginx"},
		{envoyControllerName, "envoy"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			dc := seedGateway(t, tc.controller)

			for _, name := range []string{AutoProvider, ""} {
				p, err := ResolveProvider(ctx, dc, name, "akash-gateway", "akash-gateway", logger)
				require.NoError(t, err)
				require.Equal(t, tc.want, p.Name())
			}

			p, err := ResolveProvider(ctx, dc, "nginx", "akash-gateway", "akash-gateway", logger)
			require.NoError(t, err)
			require.Equal(t, "nginx", p.Name(), "explicit name overrides detection")
		})
	}
}

// TestResolveProviderAutoErrors asserts detection fails on an unsupported
// controller and on a missing Gateway.
func TestResolveProviderAutoErrors(t *testing.T) {
	ctx := context.Background()
	logger := log.NewNopLogger()

	_, err := ResolveProvider(ctx, seedGateway(t, "example.com/other"), AutoProvider, "akash-gateway", "akash-gateway", logger)
	require.ErrorContains(t, err, "unsupported controller")

	_, err = ResolveProvider(ctx, newFakeDC(), AutoProvider, "akash-gateway", "akash-gateway", logger)
	require.Error(t, err)
}
