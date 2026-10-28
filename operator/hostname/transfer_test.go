package hostname

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/logging/logtest"
)

// TestTransferAfterFailedReconcileRemovesLiveRoute asserts that when an Envoy
// reconcile fails after its route was published, the operator still records the
// route's ownership, so moving the hostname to another lease deletes it instead of
// leaving it serving the old lease.
func TestTransferAfterFailedReconcileRemovesLiveRoute(t *testing.T) {
	ctx := context.Background()
	const hostname = "moving.example.com"

	phA := testProviderHost(t, hostname, testutil.AccAddress(t).String())
	phB := testProviderHost(t, hostname, testutil.AccAddress(t).String())
	lidA, err := phA.Spec.LeaseID()
	require.NoError(t, err)
	lidB, err := phB.Spec.LeaseID()
	require.NoError(t, err)

	manifest := func(nsName string) *crd.Manifest {
		return &crd.Manifest{
			ObjectMeta: metav1.ObjectMeta{Name: nsName, Namespace: observeTestNamespace},
			Spec: crd.ManifestSpec{Group: crd.ManifestGroup{Services: []crd.ManifestService{{
				Name:   "web",
				Count:  1,
				Expose: []crd.ManifestServiceExpose{{Port: 80, Global: true}},
			}}}},
		}
	}

	dc := migrateFakeDC()

	// The first policy create fails, after the Envoy route is already live.
	failed := false
	dc.PrependReactor("create", "backendtrafficpolicies", func(clienttesting.Action) (bool, runtime.Object, error) {
		if !failed {
			failed = true
			return true, nil, errors.New("boom")
		}
		return false, nil, nil
	})
	// Envoy accepts every policy afterwards.
	accept := func(action clienttesting.Action) (bool, runtime.Object, error) {
		if a, ok := action.(clienttesting.UpdateAction); ok {
			if obj, ok := a.GetObject().(*unstructured.Unstructured); ok {
				_ = unstructured.SetNestedSlice(obj.Object, []interface{}{map[string]interface{}{
					"controllerName": "gateway.envoyproxy.io/gatewayclass-controller",
					"ancestorRef":    map[string]interface{}{"name": "akash-gateway", "namespace": "akash-gateway"},
					"conditions":     []interface{}{map[string]interface{}{"type": "Accepted", "status": "True"}},
				}}, "status", "ancestors")
			}
		}
		return false, nil, nil
	}
	dc.PrependReactor("create", "backendtrafficpolicies", accept)
	dc.PrependReactor("update", "backendtrafficpolicies", accept)

	envoy, err := gateway.GetProvider("envoy", logtest.New())
	require.NoError(t, err)

	op := &hostnameOperator{
		ctx:               ctx,
		hostnames:         map[string]managedHostname{},
		ns:                observeTestNamespace,
		log:               logtest.New(),
		ac:                akashfake.NewSimpleClientset(manifest(builder.LidNS(lidA)), manifest(builder.LidNS(lidB))),
		dc:                dc,
		gatewayName:       "akash-gateway",
		gatewayNamespace:  "akash-gateway",
		gatewayImpl:       envoy,
		flagHostnamesData: func() {},
	}

	evA := hostnameResourceEvent{eventType: ctypes.ProviderResourceAdd, hostname: hostname, lid: lidA, serviceName: "web", externalPort: 80}
	evB := hostnameResourceEvent{eventType: ctypes.ProviderResourceAdd, hostname: hostname, lid: lidB, serviceName: "web", externalPort: 80}

	require.Error(t, op.applyAddOrUpdateEvent(ctx, evA), "the first reconcile fails")
	_, err = dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lidA)).Get(ctx, hostname, metav1.GetOptions{})
	require.NoError(t, err, "lease A's route is live despite the failure")
	require.True(t, op.hostnames[hostname].presentLease.Equals(lidA), "ownership recorded for the live route")

	require.NoError(t, op.applyAddOrUpdateEvent(ctx, evB))

	_, err = dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lidA)).Get(ctx, hostname, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err), "lease A's route must be removed on transfer")
	_, err = dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lidB)).Get(ctx, hostname, metav1.GetOptions{})
	require.NoError(t, err, "lease B now owns the hostname")
	require.True(t, op.hostnames[hostname].presentLease.Equals(lidB))
}
