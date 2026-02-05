package hostname

import (
	"context"
	"fmt"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/pager"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

var httpRouteGVROp = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

func (op *hostnameOperator) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	routeName := directive.Hostname
	ns := builder.LidNS(directive.LeaseID)

	// Validate options and log warnings
	warnings := op.gatewayImpl.ValidateOptions(directive)
	for _, warning := range warnings {
		op.log.Warn("gateway option not supported", "warning", warning)
	}

	labels := make(map[string]string)
	labels[builder.AkashManagedLabelName] = "true"
	builder.AppendLeaseLabels(directive.LeaseID, labels)

	// Use implementation to build annotations and spec
	annotations := op.gatewayImpl.BuildAnnotations(directive)
	spec := op.gatewayImpl.BuildHTTPRouteSpec(
		op.ingressConfig.GatewayName,
		op.ingressConfig.GatewayNamespace,
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
	existing, err := op.dc.Resource(httpRouteGVROp).Namespace(ns).Get(ctx, routeName, metav1.GetOptions{})

	switch {
	case err == nil:
		// Update existing
		u.SetResourceVersion(existing.GetResourceVersion())
		_, err = op.dc.Resource(httpRouteGVROp).Namespace(ns).Update(ctx, u, metav1.UpdateOptions{})
	case kerrors.IsNotFound(err):
		// Create new
		_, err = op.dc.Resource(httpRouteGVROp).Namespace(ns).Create(ctx, u, metav1.CreateOptions{})
	}

	return err
}

func (op *hostnameOperator) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)

	err := op.dc.Resource(httpRouteGVROp).Namespace(ns).Delete(ctx, hostname, metav1.DeleteOptions{})

	if err != nil && allowMissing && kerrors.IsNotFound(err) {
		return nil
	}

	return err
}

func (op *hostnameOperator) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	httpRoutePager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		unstructuredList, err := op.dc.Resource(httpRouteGVROp).Namespace(metav1.NamespaceAll).List(ctx, opts)
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

func kubeSelectorForLeaseOp(dst *strings.Builder, lID mtypes.LeaseID) {
	_, _ = fmt.Fprintf(dst, "%s=%s", builder.AkashLeaseOwnerLabelName, lID.Owner)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseDSeqLabelName, lID.DSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseGSeqLabelName, lID.GSeq)
	_, _ = fmt.Fprintf(dst, ",%s=%d", builder.AkashLeaseOSeqLabelName, lID.OSeq)
}
