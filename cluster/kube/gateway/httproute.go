package gateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

// HTTPRouteGVR is the GroupVersionResource for Gateway API HTTPRoutes.
var HTTPRouteGVR = schema.GroupVersion(gatewayv1.GroupVersion).WithResource("httproutes")

const httpRouteKind = "HTTPRoute"

var (
	// SnippetsFilterGVR is the NGF per-route filter resource.
	SnippetsFilterGVR = schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}
	// BackendTrafficPolicyGVR is the Envoy Gateway per-route policy resource.
	BackendTrafficPolicyGVR = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "backendtrafficpolicies"}

	// extensionResources maps every route extension kind any implementation creates
	// to its resource. Reconciliation deletes obsolete extensions of all of them, so a
	// route moving between implementations is left with only the current one's.
	extensionResources = map[schema.GroupVersionKind]schema.GroupVersionResource{
		SnippetsFilterGVR.GroupVersion().WithKind(snippetsFilterKind):             SnippetsFilterGVR,
		BackendTrafficPolicyGVR.GroupVersion().WithKind(backendTrafficPolicyKind): BackendTrafficPolicyGVR,
	}

	// ErrExtensionPending means the gateway controller did not report on a route
	// extension in time. The caller retries; it is not a rejection.
	ErrExtensionPending = errors.New("route extension not yet accepted by the gateway")
	// ErrExtensionRejected means the gateway controller rejected a route extension.
	ErrExtensionRejected = errors.New("route extension rejected by the gateway")
)

// ExtensionResources returns the resources of every route extension kind, sorted.
func ExtensionResources() []schema.GroupVersionResource {
	res := make([]schema.GroupVersionResource, 0, len(extensionResources))
	for _, gvr := range extensionResources {
		res = append(res, gvr)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].String() < res[j].String() })
	return res
}

// HTTPRouteConfig contains configuration for HTTPRoute operations.
type HTTPRouteConfig struct {
	GatewayName      string
	GatewayNamespace string
	Provider         GatewayProvider

	// SkipAcceptanceWait applies extensions without waiting for the gateway
	// controller to accept them. The migrate command sets it before the Gateway
	// changes class, when the target controller is not yet a parent and so never
	// reports status.
	SkipAcceptanceWait bool
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

// labelRouteExtension stamps the labels and annotation that let reconciliation and
// cleanup select every extension of a route.
func labelRouteExtension(obj *unstructured.Unstructured, routeName string, lid mtypes.LeaseID) {
	labels := map[string]string{
		builder.AkashManagedLabelName: "true",
		RouteHashLabel:                RouteHash(routeName),
	}
	builder.AppendLeaseLabels(lid, labels)
	obj.SetLabels(labels)
	obj.SetAnnotations(map[string]string{RouteNameAnnotation: routeName})
}

// CreateOrUpdateHTTPRoute creates or updates an HTTPRoute for a hostname directive
// and reconciles its full set of route extensions: the desired ones are applied,
// and every other extension of the route, from either implementation, is deleted.
//
// The order depends on the implementation (GatewayProvider.RouteFirst):
//
//   - NGF: extensions first. A route that references a missing SnippetsFilter makes
//     NGF serve HTTP 500 for that rule, so the filter is applied and accepted before
//     the route publishes its ExtensionRef. The filter is owner-referenced to the
//     route, so a brand-new route is first created as a detached placeholder (no
//     ParentRefs or rules) to mint a UID; its routable spec is attached only after
//     the filter is accepted, so a failed reconcile never exposes the backend
//     without its options.
//   - Envoy: route first. A BackendTrafficPolicy only gets status through the
//     route's parent Gateway, so a new route is created routable, then its policies
//     are applied and awaited. A route served briefly without its policy runs on
//     gateway defaults, never a 500. On an existing route the policies are applied
//     before the route update, so a tightened retry policy is in place before the
//     route gains rules that rely on it. A rejected policy is deleted, since a
//     policy that fails translation turns its route into a 500.
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
		directive,
		exts,
	)

	toUnstructured := func(s gatewayv1.HTTPRouteSpec) (*unstructured.Unstructured, error) {
		obj := &gatewayv1.HTTPRoute{
			TypeMeta: metav1.TypeMeta{
				APIVersion: gatewayv1.GroupVersion.String(),
				Kind:       httpRouteKind,
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

	u, err := toUnstructured(spec)
	if err != nil {
		return err
	}

	routes := dc.Resource(HTTPRouteGVR).Namespace(ns)
	existing, getErr := routes.Get(ctx, routeName, metav1.GetOptions{})
	if getErr != nil && !kerrors.IsNotFound(getErr) {
		return getErr
	}
	routeExists := getErr == nil

	r := routeReconcile{
		ctx:      ctx,
		dc:       dc,
		config:   config,
		ns:       ns,
		name:     routeName,
		routes:   routes,
		observer: observer,
		exists:   routeExists,
	}
	if routeExists {
		r.uid = existing.GetUID()
	}

	if config.Provider.RouteFirst() {
		return r.routeFirst(u, exts)
	}

	placeholder, err := toUnstructured(gatewayv1.HTTPRouteSpec{})
	if err != nil {
		return err
	}
	return r.extensionsFirst(u, placeholder, exts)
}

// routeReconcile carries one route's reconcile state between its steps.
type routeReconcile struct {
	ctx      context.Context
	dc       dynamic.Interface
	config   HTTPRouteConfig
	ns       string
	name     string
	routes   dynamic.ResourceInterface
	observer HTTPRouteObserver
	exists   bool
	uid      types.UID
}

func (r *routeReconcile) routeFirst(u *unstructured.Unstructured, exts []*unstructured.Unstructured) error {
	var applied []*unstructured.Unstructured

	if r.exists {
		var err error
		if applied, err = applyRouteExtensions(r.ctx, r.dc, r.ns, r.name, r.uid, exts); err != nil {
			return err
		}
		if err := r.publish(u); err != nil {
			return err
		}
	} else {
		created, err := r.routes.Create(r.ctx, u, metav1.CreateOptions{})
		r.observer.OnCreate(err)
		if err != nil {
			return err
		}
		r.uid = created.GetUID()
		if applied, err = applyRouteExtensions(r.ctx, r.dc, r.ns, r.name, r.uid, exts); err != nil {
			return err
		}
	}

	if err := removeObsoleteExtensions(r.ctx, r.dc, r.ns, r.name, r.uid, exts); err != nil {
		return err
	}

	return r.await(applied, true)
}

func (r *routeReconcile) extensionsFirst(u, placeholder *unstructured.Unstructured, exts []*unstructured.Unstructured) error {
	createdPlaceholder := false
	if !r.exists && len(exts) > 0 {
		// Create a detached placeholder with an empty spec (no ParentRefs, hostnames,
		// or rules) purely to mint a UID for the extension owner reference. It must
		// not be routable: if the extension apply, its acceptance, or the final update
		// fails, the backend must not be exposed without its http_options.
		created, err := r.routes.Create(r.ctx, placeholder, metav1.CreateOptions{})
		if err != nil {
			r.observer.OnCreate(err)
			return err
		}
		r.uid = created.GetUID()
		createdPlaceholder = true
	}

	applied, err := applyRouteExtensions(r.ctx, r.dc, r.ns, r.name, r.uid, exts)
	if err != nil {
		return err
	}

	// Wait for the gateway controller to accept every extension before the route
	// publishes its reference. NGF watches HTTPRoutes and SnippetsFilters through
	// independent caches, so publishing the reference first lets NGF reconcile the
	// route before it observes the filter and serve HTTP 500 until the caches
	// converge. An existing route keeps its previous working config meanwhile.
	if err := r.await(applied, false); err != nil {
		return err
	}

	if !r.exists && !createdPlaceholder {
		created, err := r.routes.Create(r.ctx, u, metav1.CreateOptions{})
		r.observer.OnCreate(err)
		if err != nil {
			return err
		}
		r.uid = created.GetUID()
	} else if err := r.publish(u); err != nil {
		return err
	}

	return removeObsoleteExtensions(r.ctx, r.dc, r.ns, r.name, r.uid, exts)
}

// publish updates the route to its final spec, re-reading the resourceVersion on
// each attempt: the gateway controller writes route status between our reads, so a
// cached resourceVersion can be stale and 409.
func (r *routeReconcile) publish(u *unstructured.Unstructured) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, gErr := r.routes.Get(r.ctx, r.name, metav1.GetOptions{})
		if gErr != nil {
			return gErr
		}
		u.SetResourceVersion(current.GetResourceVersion())
		_, uErr := r.routes.Update(r.ctx, u, metav1.UpdateOptions{})
		return uErr
	})
	if r.exists {
		r.observer.OnUpdate(err)
	} else {
		r.observer.OnCreate(err)
	}
	return err
}

// await waits for the gateway controller's verdict on each applied extension. A
// rejected extension that is already live (route-first ordering) is deleted, so its
// route falls back to gateway defaults instead of serving 500.
func (r *routeReconcile) await(applied []*unstructured.Unstructured, deleteRejected bool) error {
	if r.config.SkipAcceptanceWait {
		return nil
	}

	for _, ext := range applied {
		gvr := extensionResources[ext.GroupVersionKind()]
		status, err := waitForExtensionVerdict(r.ctx, r.dc, r.config, gvr, r.ns, ext.GetName())
		if err != nil {
			return err
		}

		switch status.State {
		case ExtensionAccepted:
			continue
		case ExtensionRejected:
			if deleteRejected {
				if dErr := r.dc.Resource(gvr).Namespace(r.ns).Delete(r.ctx, ext.GetName(), metav1.DeleteOptions{}); dErr != nil && !kerrors.IsNotFound(dErr) {
					return fmt.Errorf("failed to delete rejected route extension %s %q: %w", ext.GetKind(), ext.GetName(), dErr)
				}
			}
			return fmt.Errorf("%w: %s %q: %s: %s", ErrExtensionRejected, ext.GetKind(), ext.GetName(), status.Reason, status.Message)
		default:
			return fmt.Errorf("%w: %s %q", ErrExtensionPending, ext.GetKind(), ext.GetName())
		}
	}

	return nil
}

// applyRouteExtensions upserts route extensions owner-referenced to the HTTPRoute,
// so they are garbage-collected with it, and returns them as applied.
func applyRouteExtensions(ctx context.Context, dc dynamic.Interface, ns, routeName string, ownerUID types.UID, exts []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	controller := true
	applied := make([]*unstructured.Unstructured, 0, len(exts))

	for _, ext := range exts {
		ext.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       httpRouteKind,
			Name:       routeName,
			UID:        ownerUID,
			Controller: &controller,
		}})

		gvk := ext.GroupVersionKind()
		gvr, known := extensionResources[gvk]
		if !known {
			return nil, fmt.Errorf("unknown route extension kind %s", gvk)
		}

		existing, err := dc.Resource(gvr).Namespace(ns).Get(ctx, ext.GetName(), metav1.GetOptions{})
		var obj *unstructured.Unstructured
		switch {
		case err == nil:
			ext.SetResourceVersion(existing.GetResourceVersion())
			obj, err = dc.Resource(gvr).Namespace(ns).Update(ctx, ext, metav1.UpdateOptions{})
		case kerrors.IsNotFound(err):
			obj, err = dc.Resource(gvr).Namespace(ns).Create(ctx, ext, metav1.CreateOptions{})
		}
		if err != nil {
			return nil, fmt.Errorf("failed to apply route extension %s %q: %w", gvk.Kind, ext.GetName(), err)
		}

		applied = append(applied, obj)
	}

	return applied, nil
}

// removeObsoleteExtensions deletes every extension of the route that is not in the
// desired set, from either implementation, selected by the route's digest label.
// Owner references alone don't clean these up: the HTTPRoute survives. SnippetsFilters
// created before extensions were labeled are matched by the route's name, and only
// deleted when their owner reference points at this route's UID. A kind whose CRD
// is not installed is skipped.
func removeObsoleteExtensions(ctx context.Context, dc dynamic.Interface, ns, routeName string, routeUID types.UID, desired []*unstructured.Unstructured) error {
	keep := make(map[string]struct{}, len(desired))
	key := func(gvr schema.GroupVersionResource, name string) string { return gvr.String() + "/" + name }
	for _, ext := range desired {
		keep[key(extensionResources[ext.GroupVersionKind()], ext.GetName())] = struct{}{}
	}

	selector := fmt.Sprintf("%s=%s", RouteHashLabel, RouteHash(routeName))
	for _, gvr := range ExtensionResources() {
		list, err := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			if absentResource(err) {
				continue
			}
			return fmt.Errorf("failed to list route extensions %s: %w", gvr.Resource, err)
		}
		for _, item := range list.Items {
			if _, ok := keep[key(gvr, item.GetName())]; ok {
				continue
			}
			if err := deleteExtension(ctx, dc, gvr, ns, item.GetName()); err != nil {
				return err
			}
		}
	}

	if _, ok := keep[key(SnippetsFilterGVR, routeName)]; ok || routeUID == "" {
		return nil
	}

	legacy, err := dc.Resource(SnippetsFilterGVR).Namespace(ns).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		if absentResource(err) {
			return nil
		}
		return fmt.Errorf("failed to get legacy SnippetsFilter %q: %w", routeName, err)
	}
	if _, labeled := legacy.GetLabels()[RouteHashLabel]; labeled || !ownedBy(legacy, routeUID) {
		return nil
	}

	return deleteExtension(ctx, dc, SnippetsFilterGVR, ns, routeName)
}

func deleteExtension(ctx context.Context, dc dynamic.Interface, gvr schema.GroupVersionResource, ns, name string) error {
	err := dc.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete obsolete route extension %s %q: %w", gvr.Resource, name, err)
	}
	return nil
}

// absentResource reports whether err means the object or its CRD does not exist.
func absentResource(err error) bool {
	return kerrors.IsNotFound(err) || meta.IsNoMatchError(err)
}

func ownedBy(obj *unstructured.Unstructured, uid types.UID) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == uid {
			return true
		}
	}
	return false
}

// routeExtensionAcceptTimeout bounds the wait for the gateway controller's verdict
// on a route extension; on timeout the extension is reported pending, for the
// caller to retry.
var (
	routeExtensionAcceptTimeout = 15 * time.Second
	routeExtensionPollInterval  = 250 * time.Millisecond
)

// waitForExtensionVerdict polls the extension until the controller accepts or
// rejects its current generation, or the timeout passes (pending).
func waitForExtensionVerdict(
	ctx context.Context,
	dc dynamic.Interface,
	config HTTPRouteConfig,
	gvr schema.GroupVersionResource,
	ns, name string,
) (ExtensionStatus, error) {
	var status ExtensionStatus
	err := wait.PollUntilContextTimeout(ctx, routeExtensionPollInterval, routeExtensionAcceptTimeout, true, func(ctx context.Context) (bool, error) {
		obj, err := dc.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		status = config.Provider.ExtensionStatus(obj, config.GatewayNamespace, config.GatewayName)
		return status.State != ExtensionPending, nil
	})
	if err != nil && wait.Interrupted(err) && ctx.Err() == nil {
		return ExtensionStatus{State: ExtensionPending}, nil
	}
	return status, err
}

// acceptedCondition reads the Accepted condition from a condition list, judged at
// the object's current generation. A missing or zero observedGeneration is
// tolerated for controllers that do not stamp it.
func acceptedCondition(conditions []interface{}, generation int64) (ExtensionStatus, bool) {
	for _, cond := range conditions {
		condm, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _, _ := unstructured.NestedString(condm, "type")
		if typ != "Accepted" {
			continue
		}
		if obsGen, found, _ := unstructured.NestedInt64(condm, "observedGeneration"); found && obsGen != 0 && obsGen < generation {
			continue
		}

		status, _, _ := unstructured.NestedString(condm, "status")
		reason, _, _ := unstructured.NestedString(condm, "reason")
		message, _, _ := unstructured.NestedString(condm, "message")
		switch status {
		case "True":
			return ExtensionStatus{State: ExtensionAccepted, Reason: reason, Message: message}, true
		case "False":
			return ExtensionStatus{State: ExtensionRejected, Reason: reason, Message: message}, true
		}
	}
	return ExtensionStatus{State: ExtensionPending}, false
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
			if len(route.Spec.Hostnames) == 0 || len(route.Spec.Rules) == 0 {
				return nil
			}
			rule := route.Spec.Rules[0]
			if len(rule.BackendRefs) == 0 || rule.BackendRefs[0].Port == nil {
				return nil
			}
			backendRef := rule.BackendRefs[0]

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
