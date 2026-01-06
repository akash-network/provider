package kube

import (
	"context"
	"fmt"

	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/pager"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mtypes "pkg.akt.dev/go/node/market/v1"
	metricsutils "pkg.akt.dev/node/util/metrics"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

var httpRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

func (c *client) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	routeName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)

	c.log.Info("creating HTTPRoute via Gateway API",
		"route-name", routeName,
		"namespace", ns,
		"gateway-name", c.gatewayName,
		"gateway-namespace", c.gatewayNamespace,
		"implementation", c.gatewayImpl.Name())

	// Validate options and log warnings
	warnings := c.gatewayImpl.ValidateOptions(directive)
	for _, warning := range warnings {
		c.log.Warn("gateway option not supported", "warning", warning)
	}

	labels := make(map[string]string)
	labels[builder.AkashManagedLabelName] = "true"
	builder.AppendLeaseLabels(directive.LeaseID, labels)

	// Use implementation to build annotations and spec
	annotations := c.gatewayImpl.BuildAnnotations(directive)
	spec := c.gatewayImpl.BuildHTTPRouteSpec(
		c.gatewayName,
		c.gatewayNamespace,
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

	// Convert typed object to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert HTTPRoute to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: unstructuredObj}

	// Try to get existing resource
	existing, err := c.dc.Resource(httpRouteGVR).Namespace(ns).Get(ctx, routeName, metav1.GetOptions{})

	switch {
	case err == nil:
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = c.dc.Resource(httpRouteGVR).Namespace(ns).Update(ctx, u, metav1.UpdateOptions{})
		metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-update", err)
	case kubeErrors.IsNotFound(err):
		_, err = c.dc.Resource(httpRouteGVR).Namespace(ns).Create(ctx, u, metav1.CreateOptions{})
		metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-create", err)
	}

	return err
}

func (c *client) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)

	err := c.dc.Resource(httpRouteGVR).Namespace(ns).Delete(ctx, hostname, metav1.DeleteOptions{})

	if err != nil && allowMissing && kubeErrors.IsNotFound(err) {
		return nil
	}

	metricsutils.IncCounterVecWithLabelValues(kubeCallsCounter, "gateway-httproutes-delete", err)
	return err
}

func (c *client) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	httpRoutePager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		unstructuredList, err := c.dc.Resource(httpRouteGVR).Namespace(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, err
		}

		// Convert unstructured list to typed list
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

			// We only use the first hostname because the operator implementation creates
			// one HTTPRoute per hostname/domain from the SDL. Each HTTPRoute contains
			// exactly one hostname.
			if len(route.Spec.Hostnames) != 1 {
				c.log.Warn("multiple hostnames specified for HTTPRoute, ignoring additional hostnames except the first", "route-name", route.Name, "hostnames", route.Spec.Hostnames)
			}
			results = append(results, leaseIDHostnameConnection{
				leaseID:      routeLeaseID,
				hostname:     string(route.Spec.Hostnames[0]),
				externalPort: int32(*backendRef.Port),
				serviceName:  string(backendRef.Name),
			})

			return nil
		})

	if err != nil {
		return nil, err
	}

	return results, nil
}
