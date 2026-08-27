package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/pager"
	"k8s.io/client-go/util/retry"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// HTTPRouteGVR is the GroupVersionResource for Gateway API HTTPRoutes.
var HTTPRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// HTTPRouteConfig contains configuration for HTTPRoute operations.
type HTTPRouteConfig struct {
	GatewayName      string
	GatewayNamespace string
	Provider         GatewayProvider
}

// HTTPRouteObserver allows callers to observe HTTPRoute operations for metrics or logging.
type HTTPRouteObserver interface {
	OnCreate(err error)
	OnUpdate(err error)
	OnDelete(err error)
}

// NoopHTTPRouteObserver is a no-op implementation of HTTPRouteObserver.
type NoopHTTPRouteObserver struct{}

func (NoopHTTPRouteObserver) OnCreate(error) {}
func (NoopHTTPRouteObserver) OnUpdate(error) {}
func (NoopHTTPRouteObserver) OnDelete(error) {}

// CreateOrUpdateHTTPRoute creates or updates an HTTPRoute for a hostname directive.
// It uses the provided Provider to build annotations and the HTTPRoute spec.
//
// Route extensions (an NGF SnippetsFilter) are applied before the HTTPRoute
// publishes its ExtensionRef to them. A route that references a missing
// SnippetsFilter makes NGF set ResolvedRefs=False and serve HTTP 500 for that
// rule, so an existing route keeps its working spec until the filter is applied.
// The SnippetsFilter is owner-referenced to the route for garbage collection, so
// a brand-new route is first created as a detached placeholder (no ParentRefs or
// rules) to mint a UID; its routable spec is attached only after the filter is
// accepted, so a failed reconcile never exposes the backend without its options.
func CreateOrUpdateHTTPRoute(
	ctx context.Context,
	dc dynamic.Interface,
	config HTTPRouteConfig,
	directive chostname.ConnectToDeploymentDirective,
	observer HTTPRouteObserver,
) error {
	routeName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)

	labels := make(map[string]string)
	labels[builder.AkashManagedLabelName] = "true"
	builder.AppendLeaseLabels(directive.LeaseID, labels)

	annotations := config.Provider.BuildAnnotations(directive)
	exts := config.Provider.BuildRouteExtensions(ns, routeName, directive)
	spec := config.Provider.BuildHTTPRouteSpec(
		config.GatewayName,
		config.GatewayNamespace,
		directive.Hostname,
		directive.ServiceName,
		directive.ServicePort,
		exts,
	)

	toUnstructured := func(s gatewayv1.HTTPRouteSpec) (*unstructured.Unstructured, error) {
		obj := &gatewayv1.HTTPRoute{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "gateway.networking.k8s.io/v1",
				Kind:       "HTTPRoute",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:        routeName,
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: s,
		}
		m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
		}
		return &unstructured.Unstructured{Object: m}, nil
	}

	routes := dc.Resource(HTTPRouteGVR).Namespace(ns)
	existing, getErr := routes.Get(ctx, routeName, metav1.GetOptions{})
	if getErr != nil && !kerrors.IsNotFound(getErr) {
		return getErr
	}
	routeExists := getErr == nil

	var ownerUID types.UID
	createdPlaceholder := false
	if routeExists {
		ownerUID = existing.GetUID()
	} else if len(exts) > 0 {
		// Create a detached placeholder with an empty spec (no ParentRefs, hostnames,
		// or rules) purely to mint a UID for the SnippetsFilter owner reference. It
		// must not be routable: if the filter apply, its acceptance, or the final
		// update fails, the backend must not be exposed without its http_options. The
		// routable spec is attached below, only after the filter is accepted.
		placeholder, err := toUnstructured(gatewayv1.HTTPRouteSpec{})
		if err != nil {
			return err
		}
		created, err := routes.Create(ctx, placeholder, metav1.CreateOptions{})
		if err != nil {
			observer.OnCreate(err)
			return err
		}
		ownerUID = created.GetUID()
		createdPlaceholder = true
	}

	if err := applyRouteExtensions(ctx, dc, ns, routeName, ownerUID, exts); err != nil {
		return err
	}

	u, err := toUnstructured(spec)
	if err != nil {
		return err
	}

	// Brand-new route with no extensions: nothing was created above, so create it.
	if !routeExists && !createdPlaceholder {
		_, err = routes.Create(ctx, u, metav1.CreateOptions{})
		observer.OnCreate(err)
		return err
	}

	// The route already exists, or we just created a bare one. Publish the final
	// spec, re-reading the resourceVersion on each attempt: NGF writes route status
	// between our earlier reads and this update, so a cached resourceVersion can be
	// stale and 409, which would otherwise leave the route without its filter.
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, gErr := routes.Get(ctx, routeName, metav1.GetOptions{})
		if gErr != nil {
			return gErr
		}
		u.SetResourceVersion(current.GetResourceVersion())
		_, uErr := routes.Update(ctx, u, metav1.UpdateOptions{})
		return uErr
	})
	if routeExists {
		observer.OnUpdate(err)
	} else {
		observer.OnCreate(err)
	}
	return err
}

// applyRouteExtensions upserts auxiliary CRD objects (e.g. an NGF SnippetsFilter)
// owner-referenced to the HTTPRoute so they are garbage-collected with it.
func applyRouteExtensions(ctx context.Context, dc dynamic.Interface, ns, routeName string, ownerUID types.UID, exts []*unstructured.Unstructured) error {
	controller := true
	for _, ext := range exts {
		ext.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "HTTPRoute",
			Name:       routeName,
			UID:        ownerUID,
			Controller: &controller,
		}})

		gvk := ext.GroupVersionKind()
		gvr := schema.GroupVersionResource{
			Group:    gvk.Group,
			Version:  gvk.Version,
			Resource: strings.ToLower(gvk.Kind) + "s",
		}

		existing, err := dc.Resource(gvr).Namespace(ns).Get(ctx, ext.GetName(), metav1.GetOptions{})
		var applied *unstructured.Unstructured
		switch {
		case err == nil:
			ext.SetResourceVersion(existing.GetResourceVersion())
			applied, err = dc.Resource(gvr).Namespace(ns).Update(ctx, ext, metav1.UpdateOptions{})
		case kerrors.IsNotFound(err):
			applied, err = dc.Resource(gvr).Namespace(ns).Create(ctx, ext, metav1.CreateOptions{})
		}
		if err != nil {
			return fmt.Errorf("failed to apply route extension %s %q: %w", gvk.Kind, ext.GetName(), err)
		}

		// Wait for the gateway controller to accept the extension before the caller
		// publishes the HTTPRoute reference to it. NGF watches HTTPRoutes and
		// SnippetsFilters through independent caches, so publishing the reference
		// first lets NGF reconcile the route before it observes the filter, set
		// ResolvedRefs=False, and serve HTTP 500 until the caches converge. Blocking
		// here keeps an existing route on its previous working config until the
		// filter is ready; if the controller never accepts, the caller retries and
		// the route stays filter-less rather than serving 500.
		if err := waitForExtensionAccepted(ctx, dc, gvr, ns, ext.GetName(), applied.GetGeneration()); err != nil {
			return fmt.Errorf("route extension %s %q not accepted by the gateway: %w", gvk.Kind, ext.GetName(), err)
		}
	}
	return nil
}

// routeExtensionAcceptTimeout bounds the wait for the gateway controller to
// accept a route extension; on timeout applyRouteExtensions returns an error for
// the caller to retry.
var (
	routeExtensionAcceptTimeout = 15 * time.Second
	routeExtensionPollInterval  = 250 * time.Millisecond
)

// waitForExtensionAccepted polls the extension until the gateway controller
// reports Accepted=True at or beyond the applied generation.
func waitForExtensionAccepted(ctx context.Context, dc dynamic.Interface, gvr schema.GroupVersionResource, ns, name string, generation int64) error {
	return wait.PollUntilContextTimeout(ctx, routeExtensionPollInterval, routeExtensionAcceptTimeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dc.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return extensionAccepted(obj, generation), nil
	})
}

// extensionAccepted reports whether the gateway controller has set Accepted=True
// on the extension for at least the given generation. A missing or zero
// observedGeneration is tolerated so it works across controllers that do not
// stamp it.
func extensionAccepted(obj *unstructured.Unstructured, generation int64) bool {
	controllers, _, _ := unstructured.NestedSlice(obj.Object, "status", "controllers")
	for _, c := range controllers {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		conditions, _, _ := unstructured.NestedSlice(cm, "conditions")
		for _, cond := range conditions {
			condm, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}
			typ, _, _ := unstructured.NestedString(condm, "type")
			status, _, _ := unstructured.NestedString(condm, "status")
			if typ != "Accepted" || status != "True" {
				continue
			}
			if obsGen, found, _ := unstructured.NestedInt64(condm, "observedGeneration"); !found || obsGen == 0 || obsGen >= generation {
				return true
			}
		}
	}
	return false
}

// DeleteHTTPRoute removes an HTTPRoute by hostname.
// If allowMissing is true, NotFound errors are ignored.
func DeleteHTTPRoute(
	ctx context.Context,
	dc dynamic.Interface,
	namespace string,
	hostname string,
	allowMissing bool,
	observer HTTPRouteObserver,
) error {
	err := dc.Resource(HTTPRouteGVR).Namespace(namespace).Delete(ctx, hostname, metav1.DeleteOptions{})

	if err != nil && allowMissing && kerrors.IsNotFound(err) {
		observer.OnDelete(nil)
		return nil
	}

	observer.OnDelete(err)
	return err
}

// ListHTTPRouteConnections lists all Akash-managed HTTPRoutes and returns them
// as LeaseIDConnection objects.
func ListHTTPRouteConnections(
	ctx context.Context,
	dc dynamic.Interface,
) ([]chostname.LeaseIDConnection, error) {
	httpRoutePager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		unstructuredList, err := dc.Resource(HTTPRouteGVR).Namespace(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, err
		}

		routeList := &gatewayv1.HTTPRouteList{}
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredList.UnstructuredContent(), routeList)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to HTTPRouteList: %w", err)
		}

		return routeList, nil
	})

	results := make([]chostname.LeaseIDConnection, 0)
	err := httpRoutePager.EachListItem(ctx,
		metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName)},
		func(obj runtime.Object) error {
			route := obj.(*gatewayv1.HTTPRoute)
			routeLeaseID, err := clientcommon.RecoverLeaseIDFromLabels(route.Labels)
			if err != nil {
				return err
			}
			if len(route.Spec.Hostnames) == 0 {
				return fmt.Errorf("%w: no hostnames specified", kubeclienterrors.ErrInvalidHostnameConnection)
			}
			if len(route.Spec.Rules) == 0 {
				return fmt.Errorf("%w: no rules specified", kubeclienterrors.ErrInvalidHostnameConnection)
			}
			rule := route.Spec.Rules[0]
			if len(rule.BackendRefs) == 0 {
				return fmt.Errorf("%w: no backend refs", kubeclienterrors.ErrInvalidHostnameConnection)
			}
			backendRef := rule.BackendRefs[0]
			if backendRef.Port == nil {
				return fmt.Errorf("%w: backend ref has no port", kubeclienterrors.ErrInvalidHostnameConnection)
			}

			results = append(results, chostname.LeaseIDHostnameConnection{
				LeaseID:      routeLeaseID,
				Hostname:     string(route.Spec.Hostnames[0]),
				ExternalPort: int32(*backendRef.Port),
				ServiceName:  string(backendRef.Name),
			})

			return nil
		})

	if err != nil {
		return nil, err
	}

	return results, nil
}
