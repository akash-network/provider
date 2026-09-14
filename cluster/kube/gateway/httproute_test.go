package gateway

import (
	"context"
	"fmt"
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/akash-network/provider/cluster/kube/builder"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

var sfGVR = schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}

func newFakeDC() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			HTTPRouteGVR: "HTTPRouteList",
			sfGVR:        "SnippetsFilterList",
		},
	)
}

// acceptSnippetsFilters makes the fake client stamp Accepted=True on created or
// updated SnippetsFilters, mimicking NGF, so waitForExtensionAccepted returns.
func acceptSnippetsFilters(dc *dynamicfake.FakeDynamicClient) {
	accept := func(action clienttesting.Action) (bool, runtime.Object, error) {
		var obj *unstructured.Unstructured
		// clienttesting.CreateAction embeds UpdateAction, so this single case
		// matches both create and update actions (both expose GetObject).
		if a, ok := action.(clienttesting.UpdateAction); ok {
			obj, _ = a.GetObject().(*unstructured.Unstructured)
		}
		if obj != nil {
			_ = unstructured.SetNestedSlice(obj.Object, []interface{}{
				map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{"type": "Accepted", "status": "True", "observedGeneration": int64(1)},
					},
				},
			}, "status", "controllers")
		}
		return false, nil, nil
	}
	dc.PrependReactor("create", "snippetsfilters", accept)
	dc.PrependReactor("update", "snippetsfilters", accept)
}

func routeDirective() chostname.ConnectToDeploymentDirective {
	return chostname.ConnectToDeploymentDirective{
		Hostname:    "route.example.com",
		LeaseID:     mtypes.LeaseID{},
		ServiceName: "web",
		ServicePort: 80,
		MaxBodySize: 2097152,
	}
}

func routeConfig() HTTPRouteConfig {
	return HTTPRouteConfig{
		GatewayName:      "gw",
		GatewayNamespace: "gwns",
		Provider:         NewNginxGateway(log.NewNopLogger()),
	}
}

// extensionRefName returns the first ExtensionRef filter name across the route's
// rules, or "" if the route references no extension.
func extensionRefName(t *testing.T, route *unstructured.Unstructured) string {
	t.Helper()
	rules, found, err := unstructured.NestedSlice(route.Object, "spec", "rules")
	require.NoError(t, err)
	if !found {
		return ""
	}
	for _, r := range rules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		filters, ok, err := unstructured.NestedSlice(rm, "filters")
		require.NoError(t, err)
		if !ok {
			continue
		}
		for _, f := range filters {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			if name, _, _ := unstructured.NestedString(fm, "extensionRef", "name"); name != "" {
				return name
			}
		}
	}
	return ""
}

// TestCreateOrUpdateHTTPRouteAppliesFilterBeforeReference asserts a new route ends
// up referencing a SnippetsFilter that actually exists and is owned by the route,
// so NGF never sees a dangling ExtensionRef.
func TestCreateOrUpdateHTTPRouteAppliesFilterBeforeReference(t *testing.T) {
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), directive, NoopHTTPRouteObserver{}))

	sf, err := dc.Resource(sfGVR).Namespace(ns).Get(context.Background(), directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err, "SnippetsFilter must exist")

	owners := sf.GetOwnerReferences()
	require.Len(t, owners, 1)
	require.Equal(t, "HTTPRoute", owners[0].Kind)
	require.Equal(t, directive.Hostname, owners[0].Name)

	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(context.Background(), directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, directive.Hostname, extensionRefName(t, route), "route must reference the SnippetsFilter")
}

// TestCreateOrUpdateHTTPRouteDoesNotDangleOnExtensionFailure asserts that when the
// SnippetsFilter cannot be applied, an existing route is not updated to reference
// it, so live traffic never hits a missing-filter 500.
func TestCreateOrUpdateHTTPRouteDoesNotDangleOnExtensionFailure(t *testing.T) {
	dc := newFakeDC()
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)

	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion("gateway.networking.k8s.io/v1")
	existing.SetKind("HTTPRoute")
	existing.SetNamespace(ns)
	existing.SetName(directive.Hostname)
	_, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Create(context.Background(), existing, metav1.CreateOptions{})
	require.NoError(t, err)

	dc.PrependReactor("create", "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("boom")
	})

	err = CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), directive, NoopHTTPRouteObserver{})
	require.Error(t, err, "SnippetsFilter apply failure must fail the reconcile")

	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(context.Background(), directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Empty(t, extensionRefName(t, route), "existing route must not reference a SnippetsFilter that failed to apply")

	_, err = dc.Resource(sfGVR).Namespace(ns).Get(context.Background(), directive.Hostname, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err), "no SnippetsFilter should have been persisted")
}

// TestExtensionAccepted asserts the Accepted-condition parse used to gate
// publishing the route reference.
func TestExtensionAccepted(t *testing.T) {
	sf := func(condType, status string, obsGen int64) *unstructured.Unstructured {
		o := &unstructured.Unstructured{Object: map[string]interface{}{}}
		_ = unstructured.SetNestedSlice(o.Object, []interface{}{
			map[string]interface{}{"conditions": []interface{}{
				map[string]interface{}{"type": condType, "status": status, "observedGeneration": obsGen},
			}},
		}, "status", "controllers")
		return o
	}
	require.True(t, extensionAccepted(sf("Accepted", "True", 2), 2))
	require.True(t, extensionAccepted(sf("Accepted", "True", 3), 2), "newer generation counts")
	require.False(t, extensionAccepted(sf("Accepted", "True", 1), 2), "stale generation does not count")
	require.False(t, extensionAccepted(sf("Accepted", "False", 2), 2), "not accepted")
	require.False(t, extensionAccepted(sf("Programmed", "True", 2), 2), "wrong condition type")
	require.False(t, extensionAccepted(&unstructured.Unstructured{Object: map[string]interface{}{}}, 2), "no status")
}

// TestCreateOrUpdateHTTPRouteNewRoutePlaceholderNotRoutable asserts that when the
// SnippetsFilter cannot be applied for a brand-new route, the placeholder left
// behind is detached (no ParentRefs, no rules), so the backend is never exposed
// without its http_options.
func TestCreateOrUpdateHTTPRouteNewRoutePlaceholderNotRoutable(t *testing.T) {
	dc := newFakeDC()
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)

	dc.PrependReactor("create", "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("boom")
	})

	err := CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), directive, NoopHTTPRouteObserver{})
	require.Error(t, err, "SnippetsFilter apply failure must fail the reconcile")

	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(context.Background(), directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err, "the placeholder route should still exist")

	parentRefs, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	require.Empty(t, parentRefs, "placeholder must have no parentRefs (not attached to the gateway)")
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	require.Empty(t, rules, "placeholder must have no rules (backend not exposed)")
}

func TestListHTTPRouteConnectionsRecoversAfterExtensionFailure(t *testing.T) {
	for _, defaultRules := range []bool{false, true} {
		t.Run(fmt.Sprintf("defaultRules=%t", defaultRules), func(t *testing.T) {
			ctx := context.Background()
			dc := newFakeDC()
			directive := routeDirective()
			ns := builder.LidNS(directive.LeaseID)

			if defaultRules {
				// The API server defaults an omitted rules field to a path match with
				// no backend. Dynamic fake clients do not apply CRD defaults.
				dc.PrependReactor("create", "httproutes", func(action clienttesting.Action) (bool, runtime.Object, error) {
					obj := action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
					if _, found, err := unstructured.NestedSlice(obj.Object, "spec", "rules"); !found {
						require.NoError(t, err)
						require.NoError(t, unstructured.SetNestedSlice(obj.Object, []interface{}{
							map[string]interface{}{"matches": []interface{}{
								map[string]interface{}{"path": map[string]interface{}{"type": "PathPrefix", "value": "/"}},
							}},
						}, "spec", "rules"))
					}
					return false, nil, nil
				})
			}

			// A failed SnippetsFilter write leaves a detached route in Kubernetes.
			failExtension := true
			dc.PrependReactor("create", "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
				if failExtension {
					return true, nil, kerrors.NewForbidden(sfGVR.GroupResource(), directive.Hostname, fmt.Errorf("missing RBAC permission"))
				}
				return false, nil, nil
			})
			err := CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{})
			require.True(t, kerrors.IsForbidden(err), "expected the SnippetsFilter permission failure, got %v", err)

			// The operator scans connections before observing any ProviderHosts, both
			// after an event failure and on restart. The placeholder must not stop it.
			connections, err := ListHTTPRouteConnections(ctx, dc)
			require.NoError(t, err)
			require.Empty(t, connections)

			failExtension = false
			acceptSnippetsFilters(dc)
			other := directive
			other.Hostname = "other.example.com"
			other.LeaseID.DSeq++
			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), other, NoopHTTPRouteObserver{}))
			connections, err = ListHTTPRouteConnections(ctx, dc)
			require.NoError(t, err)
			require.Len(t, connections, 1, "a placeholder must not hide another lease's route")
			require.Equal(t, other.Hostname, connections[0].GetHostname())

			// Once the extension succeeds, retrying the original hostname completes it.
			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
			connections, err = ListHTTPRouteConnections(ctx, dc)
			require.NoError(t, err)
			require.Len(t, connections, 2)
			route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, directive.Hostname, extensionRefName(t, route))
		})
	}
}

func TestListHTTPRouteConnectionsRejectsAttachedRouteWithoutHostname(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))

	routes := dc.Resource(HTTPRouteGVR).Namespace(builder.LidNS(directive.LeaseID))
	route, err := routes.Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	unstructured.RemoveNestedField(route.Object, "spec", "hostnames")
	_, err = routes.Update(ctx, route, metav1.UpdateOptions{})
	require.NoError(t, err)

	// An attached route is not a placeholder; keep reporting malformed connections.
	_, err = ListHTTPRouteConnections(ctx, dc)
	require.ErrorIs(t, err, kubeclienterrors.ErrInvalidHostnameConnection)
}
