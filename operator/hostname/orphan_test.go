package hostname

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

func TestHostnameRecoveryDeletesOrphanedPlaceholderAfterRestart(t *testing.T) {
	ctx := context.Background()
	op, ac, dc := recoveryOperator(t)
	const orphanHost = "orphaned.example"
	orphanLease := seedRecoveryHost(t, ac, orphanHost, true)
	healthyLease := seedRecoveryHost(t, ac, "healthy.example", true)
	dc.PrependReactor("create", "snippetsfilters", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if obj.GetName() == orphanHost {
			return true, nil, fmt.Errorf("injected filter failure")
		}
		return false, nil, nil
	})
	orphanEvent := hostnameResourceEvent{hostname: orphanHost, leaseID: orphanLease, eventType: ctypes.ProviderResourceAdd}
	require.Error(t, op.reconcileHostname(ctx, orphanEvent))
	require.NoError(t, op.reconcileHostname(ctx, hostnameResourceEvent{hostname: "healthy.example", leaseID: healthyLease}))
	require.NoError(t, ac.AkashV2beta2().ProviderHosts("provider").Delete(ctx, orphanHost, metav1.DeleteOptions{}))
	failDelete := true
	dc.PrependReactor("delete", "httproutes", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.(ktesting.DeleteAction).GetName() == orphanHost && failDelete {
			return true, nil, fmt.Errorf("injected delete failure")
		}
		return false, nil, nil
	})
	require.Error(t, op.reconcileHostname(ctx, orphanEvent))

	// A new operator has neither the failed deletion nor the deleted ProviderHost.
	// It must rediscover the detached object from its metadata alone.
	restarted, _, _ := recoveryOperator(t)
	restarted.ac, restarted.dc = ac, dc
	require.Empty(t, restarted.pending)
	connections, err := restarted.getHostnameDeploymentConnectionsGateway(ctx)
	require.NoError(t, err)
	require.Len(t, connections, 1, "the placeholder is not a managed connection")
	require.Equal(t, "healthy.example", connections[0].GetHostname())
	require.Contains(t, restarted.pending, hostnameWorkKey{hostname: orphanHost, lease: orphanLease})
	require.Empty(t, restarted.hostnames)

	failDelete = false
	for remaining := len(restarted.pending); remaining > 0; remaining-- {
		restarted.retryHostname(ctx)
	}
	_, err = dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(orphanLease)).Get(ctx, orphanHost, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err), "the restarted operator must finish deleting the placeholder")
	require.True(t, recoveryRouteReady(dc, healthyLease, "healthy.example"), "an active route must keep serving")
}

func ownedPlaceholder(lid mtypes.LeaseID, hostname string) *unstructured.Unstructured {
	labels := map[string]string{builder.AkashManagedLabelName: "true"}
	builder.AppendLeaseLabels(lid, labels)
	route := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{}}}
	route.SetAPIVersion("gateway.networking.k8s.io/v1")
	route.SetKind("HTTPRoute")
	route.SetName(hostname)
	route.SetNamespace(builder.LidNS(lid))
	route.SetLabels(labels)
	return route
}

func TestHostnameRecoveryQueuesOnlyOwnedRouteMetadata(t *testing.T) {
	op, _, dc := recoveryOperator(t)
	lid := testutil.LeaseID(t)
	valid := ownedPlaceholder(lid, "owned.example")
	missingLabels := ownedPlaceholder(lid, "missing-labels.example")
	missingLabels.SetLabels(map[string]string{builder.AkashManagedLabelName: "true"})
	invalidLabels := ownedPlaceholder(lid, "invalid-labels.example")
	labels := invalidLabels.GetLabels()
	labels[builder.AkashLeaseDSeqLabelName] = "invalid"
	invalidLabels.SetLabels(labels)
	wrongNamespace := ownedPlaceholder(lid, "wrong-namespace.example")
	wrongNamespace.SetNamespace(builder.LidNS(testutil.LeaseID(t)))
	unmanaged := ownedPlaceholder(lid, "unmanaged.example")
	labels = unmanaged.GetLabels()
	labels[builder.AkashManagedLabelName] = "false"
	unmanaged.SetLabels(labels)
	for _, route := range []*unstructured.Unstructured{valid, missingLabels, invalidLabels, wrongNamespace, unmanaged} {
		_, err := dc.Resource(gateway.HTTPRouteGVR).Namespace(route.GetNamespace()).Create(context.Background(), route, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	connections, err := op.getHostnameDeploymentConnectionsGateway(context.Background())
	require.NoError(t, err)
	require.Empty(t, connections)
	require.Len(t, op.pending, 1)
	require.Contains(t, op.pending, hostnameWorkKey{hostname: valid.GetName(), lease: lid})
}

func TestHostnameRecoveryRouteMetadataAPIFailure(t *testing.T) {
	op, _, dc := recoveryOperator(t)
	failure := kerrors.NewForbidden(gateway.HTTPRouteGVR.GroupResource(), "", fmt.Errorf("permission denied"))
	dc.PrependReactor("list", "httproutes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, failure
	})

	_, err := op.getHostnameDeploymentConnectionsGateway(context.Background())
	require.ErrorIs(t, err, failure)
	require.Empty(t, op.pending)
}
