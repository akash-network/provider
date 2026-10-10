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
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/akash-network/provider/cluster/kube/builder"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/testutil"
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

// TestListHTTPRouteConnectionsSkipsPlaceholder asserts that a detached placeholder
// left behind by a failed reconcile does not abort listing. The hostname operator
// lists connections before it starts observing, so failing the whole list on one
// placeholder wedges the operator and no new hostname is ever routed.
func TestListHTTPRouteConnectionsSkipsPlaceholder(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	acceptSnippetsFilters(dc)

	healthy := routeDirective()
	healthy.LeaseID = testutil.LeaseID(t)
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), healthy, NoopHTTPRouteObserver{}))

	// A brand-new route whose SnippetsFilter cannot be applied leaves a placeholder.
	failFilterCreate := true
	dc.PrependReactor("create", "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
		if failFilterCreate {
			return true, nil, fmt.Errorf("boom")
		}
		return false, nil, nil
	})
	stuck := routeDirective()
	stuck.Hostname = "stuck.example.com"
	stuck.LeaseID = testutil.LeaseID(t)
	require.Error(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), stuck, NoopHTTPRouteObserver{}))
	// The API server can default rules even though the placeholder has no
	// hostname or parent. These defaults must not turn it into a connection.
	stuckNS := builder.LidNS(stuck.LeaseID)
	placeholder, err := dc.Resource(HTTPRouteGVR).Namespace(stuckNS).Get(ctx, stuck.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, unstructured.SetNestedSlice(placeholder.Object, []interface{}{
		map[string]interface{}{"matches": []interface{}{
			map[string]interface{}{"path": map[string]interface{}{"type": "PathPrefix", "value": "/"}},
		}},
	}, "spec", "rules"))
	_, err = dc.Resource(HTTPRouteGVR).Namespace(stuckNS).Update(ctx, placeholder, metav1.UpdateOptions{})
	require.NoError(t, err)

	conns, err := ListHTTPRouteConnections(ctx, dc)
	require.NoError(t, err, "a placeholder route must not abort listing")
	require.Len(t, conns, 1, "only the fully reconciled route is a connection")
	require.Equal(t, healthy.Hostname, conns[0].GetHostname())
	require.Equal(t, healthy.LeaseID, conns[0].GetLeaseID())
	require.Equal(t, healthy.ServiceName, conns[0].GetServiceName())
	require.Equal(t, healthy.ServicePort, conns[0].GetExternalPort())

	failFilterCreate = false
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), stuck, NoopHTTPRouteObserver{}))
	conns, err = ListHTTPRouteConnections(ctx, dc)
	require.NoError(t, err)
	require.Len(t, conns, 2, "retry must complete the existing placeholder after the failure clears")
	route, err := dc.Resource(HTTPRouteGVR).Namespace(stuckNS).Get(ctx, stuck.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, stuck.Hostname, extensionRefName(t, route))
}

func TestCreateOrUpdateHTTPRoutePreservesUnchangedExtension(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	before, err := dc.Resource(sfGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	dc.ClearActions()

	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	for _, action := range dc.Actions() {
		require.False(t, action.Matches("update", "snippetsfilters"), "replaying an unchanged route must retain the accepted extension")
	}
	after, err := dc.Resource(sfGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestCreateOrUpdateHTTPRouteRetriesExtensionStatusConflict(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	updates := 0
	dc.PrependReactor("update", "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			return true, nil, kerrors.NewConflict(sfGVR.GroupResource(), directive.Hostname, fmt.Errorf("controller updated status"))
		}
		return false, nil, nil
	})
	directive.MaxBodySize *= 2

	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	require.Equal(t, 2, updates)
}

func TestCreateOrUpdateHTTPRouteDefersAcceptanceAndRecovers(t *testing.T) {
	dc := newFakeDC()
	directive := routeDirective()
	directive.LeaseID = testutil.LeaseID(t)
	ns := builder.LidNS(directive.LeaseID)
	config := routeConfig()
	config.DeferExtensionAcceptance = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := CreateOrUpdateHTTPRoute(ctx, dc, config, directive, NoopHTTPRouteObserver{})
	require.ErrorIs(t, err, ErrRouteExtensionPending)
	require.NoError(t, ctx.Err(), "operator reconciliation must return without waiting for controller acceptance")
	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	parents, _, err := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	require.NoError(t, err)
	require.Empty(t, parents)
	require.Empty(t, extensionRefName(t, route), "unaccepted options must not be exposed")

	filter, err := dc.Resource(sfGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, unstructured.SetNestedSlice(filter.Object, []interface{}{
		map[string]interface{}{"conditions": []interface{}{
			map[string]interface{}{"type": "Accepted", "status": "True", "observedGeneration": filter.GetGeneration()},
		}},
	}, "status", "controllers"))
	_, err = dc.Resource(sfGVR).Namespace(ns).UpdateStatus(ctx, filter, metav1.UpdateOptions{})
	require.NoError(t, err)
	dc.ClearActions()

	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, config, directive, NoopHTTPRouteObserver{}))
	for _, action := range dc.Actions() {
		require.False(t, action.Matches("update", "snippetsfilters"), "acceptance must survive retry")
	}
	conns, err := ListHTTPRouteConnections(ctx, dc)
	require.NoError(t, err)
	require.Len(t, conns, 1)
	require.Equal(t, directive.Hostname, conns[0].GetHostname())
}

func TestCreateOrUpdateHTTPRouteRetainsRouteWhileChangedExtensionIsPending(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)
	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	before, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	dc.PrependReactor("update", "snippetsfilters", func(action clienttesting.Action) (bool, runtime.Object, error) {
		filter := action.(clienttesting.UpdateAction).GetObject().(*unstructured.Unstructured)
		filter.SetGeneration(2)
		unstructured.RemoveNestedField(filter.Object, "status")
		err := dc.Tracker().Update(sfGVR, filter, ns)
		return true, filter, err
	})
	directive.MaxBodySize *= 2
	directive.ServiceName = "new-backend"
	config := routeConfig()
	config.DeferExtensionAcceptance = true

	require.ErrorIs(t, CreateOrUpdateHTTPRoute(ctx, dc, config, directive, NoopHTTPRouteObserver{}), ErrRouteExtensionPending)
	after, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, before.Object["spec"], after.Object["spec"], "the current backend must remain until the desired filter is accepted")
}

func TestRouteExtensionRejectionIncludesDetails(t *testing.T) {
	filter := &unstructured.Unstructured{Object: map[string]interface{}{}}
	require.NoError(t, unstructured.SetNestedSlice(filter.Object, []interface{}{
		map[string]interface{}{"conditions": []interface{}{
			map[string]interface{}{
				"type": "Accepted", "status": "False", "observedGeneration": int64(4),
				"reason": "Invalid", "message": "snippets are disabled",
			},
		}},
	}, "status", "controllers"))
	err := extensionAcceptanceError(filter, 4)
	require.ErrorIs(t, err, ErrRouteExtensionRejected)
	require.NotErrorIs(t, err, ErrRouteExtensionPending, "a rejected generation must use error backoff, not rapid readiness polling")
	require.ErrorContains(t, err, "reason=Invalid")
	require.ErrorContains(t, err, "snippets are disabled")
	require.ErrorContains(t, err, "observedGeneration=4 desiredGeneration=4")

	err = extensionAcceptanceError(filter, 5)
	require.ErrorIs(t, err, ErrRouteExtensionPending, "a stale rejection does not describe the new generation")
	require.NotErrorIs(t, err, ErrRouteExtensionRejected)
}

func TestListHTTPRouteConnectionsSkipsDetachedRoutes(t *testing.T) {
	for _, field := range []string{"parentRefs", "hostnames"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			dc := newFakeDC()
			acceptSnippetsFilters(dc)
			directive := routeDirective()
			directive.LeaseID = testutil.LeaseID(t)
			ns := builder.LidNS(directive.LeaseID)
			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
			route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
			require.NoError(t, err)
			unstructured.RemoveNestedField(route.Object, "spec", field)
			_, err = dc.Resource(HTTPRouteGVR).Namespace(ns).Update(ctx, route, metav1.UpdateOptions{})
			require.NoError(t, err)

			conns, err := ListHTTPRouteConnections(ctx, dc)
			require.NoError(t, err)
			require.Empty(t, conns)
		})
	}
}

func TestListHTTPRouteConnectionsSkipsMalformedRoutes(t *testing.T) {
	cases := map[string]func(*testing.T, *unstructured.Unstructured){
		"lease labels": func(_ *testing.T, route *unstructured.Unstructured) {
			route.SetLabels(map[string]string{builder.AkashManagedLabelName: "true"})
		},
		"lease namespace": func(t *testing.T, route *unstructured.Unstructured) {
			labels := route.GetLabels()
			builder.AppendLeaseLabels(testutil.LeaseID(t), labels)
			route.SetLabels(labels)
		},
		"rules": func(_ *testing.T, route *unstructured.Unstructured) {
			unstructured.RemoveNestedField(route.Object, "spec", "rules")
		},
		"backend references": func(t *testing.T, route *unstructured.Unstructured) {
			require.NoError(t, unstructured.SetNestedSlice(route.Object, []interface{}{map[string]interface{}{}}, "spec", "rules"))
		},
		"backend port": func(t *testing.T, route *unstructured.Unstructured) {
			require.NoError(t, unstructured.SetNestedSlice(route.Object, []interface{}{
				map[string]interface{}{"backendRefs": []interface{}{map[string]interface{}{"name": "web"}}},
			}, "spec", "rules"))
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dc := newFakeDC()
			acceptSnippetsFilters(dc)
			healthy := routeDirective()
			healthy.LeaseID = testutil.LeaseID(t)
			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), healthy, NoopHTTPRouteObserver{}))
			broken := healthy
			broken.Hostname = "broken.example.com"
			ns := builder.LidNS(broken.LeaseID)
			require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), broken, NoopHTTPRouteObserver{}))
			route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, broken.Hostname, metav1.GetOptions{})
			require.NoError(t, err)
			corrupt(t, route)
			_, err = dc.Resource(HTTPRouteGVR).Namespace(ns).Update(ctx, route, metav1.UpdateOptions{})
			require.NoError(t, err)

			conns, err := ListHTTPRouteConnections(ctx, dc)
			require.NoError(t, err, "one incomplete route must not prevent recovery for every hostname")
			require.Len(t, conns, 1)
			require.Equal(t, healthy.Hostname, conns[0].GetHostname())
		})
	}
}

func TestCreateOrUpdateHTTPRouteWaitsForAcceptanceByDefault(t *testing.T) {
	ctx := context.Background()
	dc := newFakeDC()
	directive := routeDirective()
	ns := builder.LidNS(directive.LeaseID)
	reads := 0
	dc.PrependReactor("get", "snippetsfilters", func(action clienttesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads == 1 {
			return false, nil, nil
		}
		obj, err := dc.Tracker().Get(sfGVR, ns, action.(clienttesting.GetAction).GetName())
		require.NoError(t, err)
		filter := obj.(*unstructured.Unstructured)
		require.NoError(t, unstructured.SetNestedSlice(filter.Object, []interface{}{
			map[string]interface{}{"conditions": []interface{}{
				map[string]interface{}{"type": "Accepted", "status": "True", "observedGeneration": filter.GetGeneration()},
			}},
		}, "status", "controllers"))
		return true, filter, nil
	})

	require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
	require.Equal(t, 2, reads, "the synchronous caller must observe acceptance after creating its filter")
	route, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, directive.Hostname, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, directive.Hostname, extensionRefName(t, route))
}

func TestCreateOrUpdateHTTPRouteReturnsCanceledExtensionRequest(t *testing.T) {
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, verb := range []string{"get", "create", "update"} {
			t.Run(failure.Error()+"/"+verb, func(t *testing.T) {
				ctx := context.Background()
				dc := newFakeDC()
				directive := routeDirective()
				if verb == "update" {
					acceptSnippetsFilters(dc)
					require.NoError(t, CreateOrUpdateHTTPRoute(ctx, dc, routeConfig(), directive, NoopHTTPRouteObserver{}))
					directive.MaxBodySize *= 2
				}
				dc.PrependReactor(verb, "snippetsfilters", func(clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, failure
				})
				config := routeConfig()
				config.DeferExtensionAcceptance = true

				require.ErrorIs(t, CreateOrUpdateHTTPRoute(ctx, dc, config, directive, NoopHTTPRouteObserver{}), failure)
			})
		}
	}
}

func TestCreateOrUpdateHTTPRouteReturnsCanceledPublish(t *testing.T) {
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	dc.PrependReactor("update", "httproutes", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.Canceled
	})
	require.ErrorIs(t, CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), routeDirective(), NoopHTTPRouteObserver{}), context.Canceled)
}

func TestCreateOrUpdateHTTPRoutePreservesCancellationAfterConflict(t *testing.T) {
	dc := newFakeDC()
	acceptSnippetsFilters(dc)
	directive := routeDirective()
	attempts := 0
	dc.PrependReactor("update", "httproutes", func(clienttesting.Action) (bool, runtime.Object, error) {
		attempts++
		if attempts == 1 {
			return true, nil, kerrors.NewConflict(HTTPRouteGVR.GroupResource(), directive.Hostname, fmt.Errorf("controller updated status"))
		}
		return true, nil, context.Canceled
	})
	require.ErrorIs(t, CreateOrUpdateHTTPRoute(context.Background(), dc, routeConfig(), directive, NoopHTTPRouteObserver{}), context.Canceled)
	require.Equal(t, 2, attempts)
}

func TestListHTTPRouteConnectionsReturnsAPIFailure(t *testing.T) {
	dc := newFakeDC()
	failure := kerrors.NewForbidden(HTTPRouteGVR.GroupResource(), "", fmt.Errorf("permission denied"))
	dc.PrependReactor("list", "httproutes", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, failure
	})

	_, err := ListHTTPRouteConnections(context.Background(), dc)
	require.ErrorIs(t, err, failure)
}
