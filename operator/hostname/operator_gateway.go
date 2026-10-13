package hostname

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/pager"

	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/clientcommon"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

func (op *hostnameOperator) connectHostnameToDeploymentGateway(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error {
	config := gateway.HTTPRouteConfig{
		GatewayName:              op.ingressConfig.GatewayName,
		GatewayNamespace:         op.ingressConfig.GatewayNamespace,
		Provider:                 op.gatewayImpl,
		DeferExtensionAcceptance: true,
	}

	return gateway.CreateOrUpdateHTTPRoute(ctx, op.dc, config, directive, gateway.NoopHTTPRouteObserver{})
}

func (op *hostnameOperator) removeHostnameFromDeploymentGateway(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error {
	ns := builder.LidNS(leaseID)
	return gateway.DeleteHTTPRoute(ctx, op.dc, ns, hostname, allowMissing, gateway.NoopHTTPRouteObserver{})
}

func (op *hostnameOperator) getHostnameDeploymentConnectionsGateway(ctx context.Context) ([]chostname.LeaseIDConnection, error) {
	// Recover work from metadata too. A deleted ProviderHost can leave a detached
	// placeholder whose failed deletion was lost when the operator restarted.
	// These objects are work to recheck, not established hostname connections.
	routePager := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return op.dc.Resource(gateway.HTTPRouteGVR).Namespace(metav1.NamespaceAll).List(ctx, opts)
	})
	var work []hostnameResourceEvent
	err := routePager.EachListItem(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", builder.AkashManagedLabelName),
	}, func(obj runtime.Object) error {
		route, ok := obj.(*unstructured.Unstructured)
		if !ok || route == nil {
			return nil
		}
		if route.GetLabels()[builder.AkashManagedLabelName] != "true" || route.GetName() == "" {
			return nil
		}
		lease, err := clientcommon.RecoverLeaseIDFromLabels(route.GetLabels())
		if err != nil {
			op.log.Error("unable to recover hostname route identity", "namespace", route.GetNamespace(), "route", route.GetName(), "err", err)
			return nil
		}
		if err := lease.Validate(); err != nil {
			op.log.Error("invalid hostname route lease", "namespace", route.GetNamespace(), "route", route.GetName(), "err", err)
			return nil
		}
		if route.GetNamespace() != builder.LidNS(lease) {
			op.log.Error("hostname route namespace does not match lease", "namespace", route.GetNamespace(), "route", route.GetName())
			return nil
		}
		work = append(work, hostnameResourceEvent{
			eventType: ctypes.ProviderResourceUpdate, hostname: route.GetName(), leaseID: lease,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, ev := range work {
		op.queueHostname(ev)
	}
	return gateway.ListHTTPRouteConnections(ctx, op.dc)
}
