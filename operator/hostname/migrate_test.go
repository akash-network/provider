package hostname

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/logging/logtest"
)

func migrateFakeDC() *dynamicfake.FakeDynamicClient {
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			gateway.HTTPRouteGVR:            "HTTPRouteList",
			gateway.SnippetsFilterGVR:       "SnippetsFilterList",
			gateway.BackendTrafficPolicyGVR: "BackendTrafficPolicyList",
		},
	)

	// NGF accepts every SnippetsFilter, so the rollback direction can complete.
	accept := func(action clienttesting.Action) (bool, runtime.Object, error) {
		if a, ok := action.(clienttesting.UpdateAction); ok {
			if obj, ok := a.GetObject().(*unstructured.Unstructured); ok {
				_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
					map[string]interface{}{"conditions": []interface{}{
						map[string]interface{}{"type": "Accepted", "status": "True"},
					}},
				}, "status", "controllers")
			}
		}
		return false, nil, nil
	}
	dc.PrependReactor("create", "snippetsfilters", accept)
	dc.PrependReactor("update", "snippetsfilters", accept)

	return dc
}

func migrateFixtures(t *testing.T) (*akashfake.Clientset, *crd.ProviderHost) {
	t.Helper()

	ph := testProviderHost(t, "web.example.com", testutil.AccAddress(t).String())
	lid, err := ph.Spec.LeaseID()
	require.NoError(t, err)

	mani := &crd.Manifest{
		ObjectMeta: metav1.ObjectMeta{Name: builder.LidNS(lid), Namespace: observeTestNamespace},
		Spec: crd.ManifestSpec{Group: crd.ManifestGroup{Services: []crd.ManifestService{{
			Name:   "web",
			Count:  1,
			Expose: []crd.ManifestServiceExpose{{Port: 80, Global: true}},
		}}}},
	}

	orphan := testProviderHost(t, "orphan.example.com", testutil.AccAddress(t).String())
	orphan.Name = "orphan.example.com"

	return akashfake.NewSimpleClientset(ph, mani, orphan), ph
}

func migrateConfig(ac *akashfake.Clientset, dc *dynamicfake.FakeDynamicClient, target string) MigrateConfig {
	return MigrateConfig{
		Logger:           logtest.New(),
		Akash:            ac,
		Dynamic:          dc,
		ManifestNS:       observeTestNamespace,
		GatewayName:      "akash-gateway",
		GatewayNamespace: "akash-gateway",
		Target:           target,
		Concurrency:      4,
	}
}

func names(t *testing.T, dc *dynamicfake.FakeDynamicClient, gvr schema.GroupVersionResource) []string {
	t.Helper()
	list, err := dc.Resource(gvr).Namespace(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	out := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, item.GetName())
	}
	return out
}

// TestMigrateRoutesNginxToEnvoyAndBack asserts NGF-shaped routes move to the Envoy
// shape with the inventory gate passing, a ProviderHost without a manifest is
// reported rather than failed, and the reverse direction restores the NGF shape.
func TestMigrateRoutesNginxToEnvoyAndBack(t *testing.T) {
	ctx := context.Background()
	ac, ph := migrateFixtures(t)
	dc := migrateFakeDC()

	report, err := MigrateRoutes(ctx, migrateConfig(ac, dc, "nginx"))
	require.NoError(t, err)
	require.True(t, report.Ready(), "%+v", report)
	require.Equal(t, []string{ph.Spec.Hostname}, names(t, dc, gateway.SnippetsFilterGVR))

	report, err = MigrateRoutes(ctx, migrateConfig(ac, dc, "envoy"))
	require.NoError(t, err)
	require.True(t, report.Ready(), "%+v", report)
	require.Equal(t, []string{ph.Spec.Hostname}, report.Migrated)
	require.Equal(t, []string{"orphan.example.com"}, report.NoManifest)
	require.Empty(t, names(t, dc, gateway.SnippetsFilterGVR), "NGF filters removed")
	require.ElementsMatch(t, []string{ph.Spec.Hostname, ph.Spec.Hostname + "-idempotent"}, names(t, dc, gateway.BackendTrafficPolicyGVR))

	route, err := dc.Resource(gateway.HTTPRouteGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, route.Items, 1)
	require.False(t, referencesSnippetsFilter(route.Items[0]))

	report, err = MigrateRoutes(ctx, migrateConfig(ac, dc, "nginx"))
	require.NoError(t, err)
	require.True(t, report.Ready(), "%+v", report)
	require.Empty(t, names(t, dc, gateway.BackendTrafficPolicyGVR), "Envoy policies removed on rollback")
	require.Equal(t, []string{ph.Spec.Hostname}, names(t, dc, gateway.SnippetsFilterGVR))
}

// TestMigrateRoutesDryRun asserts a dry run reports the plan and writes nothing.
func TestMigrateRoutesDryRun(t *testing.T) {
	ac, ph := migrateFixtures(t)
	dc := migrateFakeDC()

	cfg := migrateConfig(ac, dc, "envoy")
	cfg.DryRun = true
	report, err := MigrateRoutes(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, []string{ph.Spec.Hostname}, report.Migrated)
	require.Empty(t, names(t, dc, gateway.HTTPRouteGVR))
	require.Empty(t, names(t, dc, gateway.BackendTrafficPolicyGVR))
}

// TestMigrateRoutesGateBlocksOnFilterReference asserts the inventory flags a route
// still referencing a SnippetsFilter, which Envoy would serve as 500.
func TestMigrateRoutesGateBlocksOnFilterReference(t *testing.T) {
	ctx := context.Background()
	ac := akashfake.NewSimpleClientset()
	dc := migrateFakeDC()

	stray := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]interface{}{
			"name":      "stray.example.com",
			"namespace": "lease-ns",
			"labels":    map[string]interface{}{builder.AkashManagedLabelName: "true"},
		},
		"spec": map[string]interface{}{"rules": []interface{}{map[string]interface{}{
			"filters": []interface{}{map[string]interface{}{
				"type":         "ExtensionRef",
				"extensionRef": map[string]interface{}{"group": "gateway.nginx.org", "kind": "SnippetsFilter", "name": "stray.example.com"},
			}},
		}}},
	}}
	_, err := dc.Resource(gateway.HTTPRouteGVR).Namespace("lease-ns").Create(ctx, stray, metav1.CreateOptions{})
	require.NoError(t, err)

	report, err := MigrateRoutes(ctx, migrateConfig(ac, dc, "envoy"))
	require.NoError(t, err)
	require.False(t, report.Ready())
	require.Equal(t, []string{"lease-ns/stray.example.com"}, report.Incompatible)
}
