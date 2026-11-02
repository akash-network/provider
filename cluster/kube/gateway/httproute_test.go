package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/akash-network/provider/cluster/kube/builder"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	mtypes "pkg.akt.dev/go/node/market/v1"
)

var sfGVR = schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}

func newFakeDC() *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			HTTPRouteGVR:            "HTTPRouteList",
			sfGVR:                   "SnippetsFilterList",
			BackendTrafficPolicyGVR: "BackendTrafficPolicyList",
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

// TestNginxExtensionStatus asserts NGF's verdict is read from status.controllers at
// the object's current generation, ignoring other controllers.
func TestNginxExtensionStatus(t *testing.T) {
	gw := NewNginxGateway(log.NewNopLogger())
	sf := func(controller, condType, status string, obsGen, gen int64) *unstructured.Unstructured {
		o := &unstructured.Unstructured{Object: map[string]interface{}{}}
		o.SetGeneration(gen)
		entry := map[string]interface{}{"conditions": []interface{}{
			map[string]interface{}{"type": condType, "status": status, "observedGeneration": obsGen, "reason": "R", "message": "M"},
		}}
		if controller != "" {
			entry["controllerName"] = controller
		}
		_ = unstructured.SetNestedSlice(o.Object, []interface{}{entry}, "status", "controllers")
		return o
	}
	state := func(o *unstructured.Unstructured) ExtensionState { return gw.ExtensionStatus(o, "gwns", "gw").State }

	require.Equal(t, ExtensionAccepted, state(sf("", "Accepted", "True", 2, 2)))
	require.Equal(t, ExtensionAccepted, state(sf(nginxControllerName, "Accepted", "True", 3, 2)), "newer generation counts")
	require.Equal(t, ExtensionPending, state(sf("", "Accepted", "True", 1, 2)), "stale generation does not count")
	require.Equal(t, ExtensionRejected, state(sf("", "Accepted", "False", 2, 2)))
	require.Equal(t, ExtensionPending, state(sf("", "Programmed", "True", 2, 2)), "wrong condition type")
	require.Equal(t, ExtensionPending, state(sf("other/controller", "Accepted", "True", 2, 2)), "other controller")
	require.Equal(t, ExtensionPending, state(&unstructured.Unstructured{Object: map[string]interface{}{}}), "no status")

	rejected := gw.ExtensionStatus(sf("", "Accepted", "False", 2, 2), "gwns", "gw")
	require.Equal(t, "R", rejected.Reason)
	require.Equal(t, "M", rejected.Message)
}

// btpWithAncestor returns a BTP whose status carries one ancestor entry.
func btpWithAncestor(controller, gwNS, gwName, status string, gen int64) *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]interface{}{}}
	o.SetGeneration(gen)
	_ = unstructured.SetNestedSlice(o.Object, []interface{}{
		map[string]interface{}{
			"controllerName": controller,
			"ancestorRef":    map[string]interface{}{"group": "gateway.networking.k8s.io", "kind": "Gateway", "namespace": gwNS, "name": gwName},
			"conditions": []interface{}{
				map[string]interface{}{"type": "Accepted", "status": status, "observedGeneration": int64(1), "reason": "Invalid", "message": "bad"},
			},
		},
	}, "status", "ancestors")
	return o
}

// TestEnvoyExtensionStatus asserts Envoy's verdict is read only from its own
// controller's ancestor entry for the given Gateway, at the current generation.
func TestEnvoyExtensionStatus(t *testing.T) {
	gw := NewEnvoyGateway(log.NewNopLogger())
	state := func(o *unstructured.Unstructured) ExtensionState { return gw.ExtensionStatus(o, "gwns", "gw").State }

	require.Equal(t, ExtensionAccepted, state(btpWithAncestor(envoyControllerName, "gwns", "gw", "True", 1)))
	require.Equal(t, ExtensionRejected, state(btpWithAncestor(envoyControllerName, "gwns", "gw", "False", 1)))
	require.Equal(t, ExtensionPending, state(btpWithAncestor(envoyControllerName, "gwns", "gw", "True", 2)), "stale generation")
	require.Equal(t, ExtensionPending, state(btpWithAncestor(nginxControllerName, "gwns", "gw", "True", 1)), "other controller")
	require.Equal(t, ExtensionPending, state(btpWithAncestor(envoyControllerName, "gwns", "other", "True", 1)), "other gateway")
	require.Equal(t, ExtensionPending, state(btpWithAncestor(envoyControllerName, "otherns", "gw", "True", 1)), "other namespace")
	require.Equal(t, ExtensionPending, state(&unstructured.Unstructured{Object: map[string]interface{}{}}), "no status")
}

func envoyConfig() HTTPRouteConfig {
	return HTTPRouteConfig{
		GatewayName:      "gw",
		GatewayNamespace: "gwns",
		Provider:         NewEnvoyGateway(log.NewNopLogger()),
	}
}

func envoyDirective() chostname.ConnectToDeploymentDirective {
	d := routeDirective()
	d.ReadTimeout, d.SendTimeout, d.NextTries = 60000, 60000, 3
	d.NextCases = []string{"error", "timeout"}
	return d
}

// stampBTPs makes the fake client set Envoy's verdict on created or updated BTPs.
func stampBTPs(dc *dynamicfake.FakeDynamicClient, status string) {
	stamp := func(action clienttesting.Action) (bool, runtime.Object, error) {
		if a, ok := action.(clienttesting.UpdateAction); ok {
			if obj, ok := a.GetObject().(*unstructured.Unstructured); ok {
				s := btpWithAncestor(envoyControllerName, "gwns", "gw", status, 1)
				obj.Object["status"] = s.Object["status"]
			}
		}
		return false, nil, nil
	}
	dc.PrependReactor("create", "backendtrafficpolicies", stamp)
	dc.PrependReactor("update", "backendtrafficpolicies", stamp)
}

func listNames(t *testing.T, dc *dynamicfake.FakeDynamicClient, gvr schema.GroupVersionResource, ns string) []string {
	t.Helper()
	list, err := dc.Resource(gvr).Namespace(ns).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names
}

// TestEnvoyNewRouteCreatedRoutableFirst asserts a new Envoy route is created
// routable before its policies, which then carry an owner reference to it, and
// that the reconcile succeeds once Envoy accepts them.
func TestEnvoyNewRouteCreatedRoutableFirst(t *testing.T) {
	dc := newFakeDC()
	stampBTPs(dc, "True")
	d := envoyDirective()
	ns := builder.LidNS(d.LeaseID)

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{}))

	var order []string
	for _, a := range dc.Actions() {
		if a.GetVerb() == "create" {
			order = append(order, a.GetResource().Resource)
		}
	}
	require.Equal(t, []string{"httproutes", "backendtrafficpolicies", "backendtrafficpolicies"}, order)

	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(context.Background(), d.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	parentRefs, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	require.Len(t, parentRefs, 1, "the route is routable from the start")
	require.Empty(t, extensionRefName(t, route))

	require.ElementsMatch(t, []string{d.Hostname, d.Hostname + "-idempotent"}, listNames(t, dc, BackendTrafficPolicyGVR, ns))
	btp, err := dc.Resource(BackendTrafficPolicyGVR).Namespace(ns).Get(context.Background(), d.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, d.Hostname, btp.GetOwnerReferences()[0].Name)
}

// TestEnvoyRejectedPolicyDeleted asserts a policy Envoy rejects is deleted, so the
// route falls back to gateway defaults rather than serving 500, and the reconcile
// reports the rejection.
func TestEnvoyRejectedPolicyDeleted(t *testing.T) {
	dc := newFakeDC()
	stampBTPs(dc, "False")
	d := envoyDirective()
	ns := builder.LidNS(d.LeaseID)

	err := CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{})
	require.ErrorIs(t, err, ErrExtensionRejected)

	_, err = dc.Resource(BackendTrafficPolicyGVR).Namespace(ns).Get(context.Background(), d.Hostname, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err), "rejected policy must be deleted")
}

// TestEnvoyPendingPolicyReported asserts a policy with no verdict is reported
// pending, distinct from a rejection, and left in place.
func TestEnvoyPendingPolicyReported(t *testing.T) {
	prev := routeExtensionAcceptTimeout
	routeExtensionAcceptTimeout = 300 * time.Millisecond
	t.Cleanup(func() { routeExtensionAcceptTimeout = prev })

	dc := newFakeDC()
	d := envoyDirective()
	ns := builder.LidNS(d.LeaseID)

	err := CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{})
	require.ErrorIs(t, err, ErrExtensionPending)
	require.NotErrorIs(t, err, ErrExtensionRejected)

	_, err = dc.Resource(BackendTrafficPolicyGVR).Namespace(ns).Get(context.Background(), d.Hostname, metav1.GetOptions{})
	require.NoError(t, err, "pending policy stays")
}

// TestSkipAcceptanceWait asserts the migrate mode applies policies without waiting
// for a verdict.
func TestSkipAcceptanceWait(t *testing.T) {
	dc := newFakeDC()
	cfg := envoyConfig()
	cfg.SkipAcceptanceWait = true

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, cfg, envoyDirective(), NoopHTTPRouteObserver{}))
}

// TestEnvoyExistingRoutePoliciesBeforeRoute asserts that on an existing route the
// policies are written before the route update, so a tightened retry policy is in
// place before the route gains rules that rely on it.
func TestEnvoyExistingRoutePoliciesBeforeRoute(t *testing.T) {
	dc := newFakeDC()
	stampBTPs(dc, "True")
	d := envoyDirective()
	d.NextCases = []string{"error", "non_idempotent"}
	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{}))

	dc.ClearActions()
	d.NextCases = []string{"error"}
	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{}))

	var writes []string
	for _, a := range dc.Actions() {
		if v := a.GetVerb(); v == "create" || v == "update" {
			writes = append(writes, v+" "+a.GetResource().Resource)
		}
	}
	require.Equal(t, []string{
		"update backendtrafficpolicies",
		"create backendtrafficpolicies",
		"update httproutes",
	}, writes)
}

// TestReconcileRemovesObsoleteExtensions asserts that a route moving from NGF to
// Envoy loses its SnippetsFilter, and an Envoy route that stops being split loses
// its idempotent-rule policy.
func TestReconcileRemovesObsoleteExtensions(t *testing.T) {
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	stampBTPs(dc, "True")
	d := envoyDirective()
	ns := builder.LidNS(d.LeaseID)

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), d, NoopHTTPRouteObserver{}))
	require.Equal(t, []string{d.Hostname}, listNames(t, dc, sfGVR, ns))

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{}))
	require.Empty(t, listNames(t, dc, sfGVR, ns), "NGF filter removed after moving to Envoy")
	require.Len(t, listNames(t, dc, BackendTrafficPolicyGVR, ns), 2)

	d.NextCases = []string{"off"}
	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, envoyConfig(), d, NoopHTTPRouteObserver{}))
	require.Equal(t, []string{d.Hostname}, listNames(t, dc, BackendTrafficPolicyGVR, ns), "idempotent policy removed when unsplit")

	require.NoError(t, CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), d, NoopHTTPRouteObserver{}))
	require.Empty(t, listNames(t, dc, BackendTrafficPolicyGVR, ns), "Envoy policies removed after moving back to NGF")
	require.Equal(t, []string{d.Hostname}, listNames(t, dc, sfGVR, ns))
}

// TestReconcileRemovesLegacyFilterOnlyWhenOwned asserts an unlabeled SnippetsFilter
// from before extensions were labeled is deleted by route name only when its owner
// reference points at the route.
func TestReconcileRemovesLegacyFilterOnlyWhenOwned(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ownRoute bool
		wantGone bool
	}{
		{"owned by the route", true, true},
		{"owned by something else", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := newFakeDC()
			stampBTPs(dc, "True")
			d := envoyDirective()
			ns := builder.LidNS(d.LeaseID)
			ctx := context.Background()

			route := &unstructured.Unstructured{}
			route.SetAPIVersion("gateway.networking.k8s.io/v1")
			route.SetKind("HTTPRoute")
			route.SetNamespace(ns)
			route.SetName(d.Hostname)
			route.SetUID("route-uid")
			_, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Create(ctx, route, metav1.CreateOptions{})
			require.NoError(t, err)

			owner := types.UID("someone-else")
			if tc.ownRoute {
				owner = "route-uid"
			}
			legacy := &unstructured.Unstructured{}
			legacy.SetAPIVersion("gateway.nginx.org/v1alpha1")
			legacy.SetKind(snippetsFilterKind)
			legacy.SetNamespace(ns)
			legacy.SetName(d.Hostname)
			legacy.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "gateway.networking.k8s.io/v1", Kind: "HTTPRoute", Name: d.Hostname, UID: owner}})
			_, err = dc.Resource(sfGVR).Namespace(ns).Create(ctx, legacy, metav1.CreateOptions{})
			require.NoError(t, err)

			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, envoyConfig(), d, NoopHTTPRouteObserver{}))

			_, err = dc.Resource(sfGVR).Namespace(ns).Get(ctx, d.Hostname, metav1.GetOptions{})
			require.Equal(t, tc.wantGone, kerrors.IsNotFound(err))
		})
	}
}

// TestNginxFilterLabeled asserts NGF filters carry the route labels, so cleanup can
// select them.
func TestNginxFilterLabeled(t *testing.T) {
	exts := NewNginxGateway(log.NewNopLogger()).BuildRouteExtensions("ns1", "route.example.com", routeDirective())
	require.Len(t, exts, 1)
	require.Equal(t, RouteHash("route.example.com"), exts[0].GetLabels()[RouteHashLabel])
	require.Equal(t, "true", exts[0].GetLabels()[builder.AkashManagedLabelName])
	require.Equal(t, "route.example.com", exts[0].GetAnnotations()[RouteNameAnnotation])
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

// TestListHTTPRouteConnectionsSkipsPlaceholder asserts that a stranded detached
// placeholder route (no hostnames) is skipped rather than aborting the whole
// listing, so one incomplete route cannot poison the operator's reconcile loop.
func TestListHTTPRouteConnectionsSkipsPlaceholder(t *testing.T) {
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	ctx := context.Background()

	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), routeDirective(), NoopHTTPRouteObserver{}))

	ns := builder.LidNS(mtypes.LeaseID{})
	labels := builder.AppendLeaseLabels(mtypes.LeaseID{}, map[string]string{builder.AkashManagedLabelName: "true"})
	lm := make(map[string]interface{}, len(labels))
	for k, v := range labels {
		lm[k] = v
	}
	placeholder := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]interface{}{
			"name":      "placeholder.example.com",
			"namespace": ns,
			"labels":    lm,
		},
		"spec": map[string]interface{}{},
	}}
	_, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Create(ctx, placeholder, metav1.CreateOptions{})
	require.NoError(t, err)

	conns, err := ListHTTPRouteConnections(ctx, dc)
	require.NoError(t, err, "a placeholder route must not abort listing")
	require.Len(t, conns, 1)
	require.Equal(t, routeDirective().Hostname, conns[0].GetHostname())
}
