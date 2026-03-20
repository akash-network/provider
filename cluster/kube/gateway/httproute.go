package gateway

import (
	"context"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/pager"
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
	Implementation   Implementation
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
// It uses the provided Implementation to build annotations and the HTTPRoute spec.
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

	annotations := config.Implementation.BuildAnnotations(directive)
	spec := config.Implementation.BuildHTTPRouteSpec(
		config.GatewayName,
		config.GatewayNamespace,
		directive.Hostname,
		directive.ServiceName,
		directive.ServicePort,
		directive,
	)

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
		Spec: spec,
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: unstructuredObj}

	existing, err := dc.Resource(HTTPRouteGVR).Namespace(ns).Get(ctx, routeName, metav1.GetOptions{})

	switch {
	case err == nil:
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = dc.Resource(HTTPRouteGVR).Namespace(ns).Update(ctx, u, metav1.UpdateOptions{})
		observer.OnUpdate(err)
	case kerrors.IsNotFound(err):
		_, err = dc.Resource(HTTPRouteGVR).Namespace(ns).Create(ctx, u, metav1.CreateOptions{})
		observer.OnCreate(err)
	}

	return err
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
