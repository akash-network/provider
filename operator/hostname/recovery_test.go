package hostname

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube"
	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/operator/common"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

var recoveryFilterGVR = schema.GroupVersionResource{Group: "gateway.nginx.org", Version: "v1alpha1", Resource: "snippetsfilters"}

func recoveryOperator(t *testing.T) (*hostnameOperator, *afake.Clientset, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ac := afake.NewSimpleClientset()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		gateway.HTTPRouteGVR: "HTTPRouteList", recoveryFilterGVR: "SnippetsFilterList",
	})
	accept := func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.UpdateAction).GetObject().(*unstructured.Unstructured)
		err := unstructured.SetNestedSlice(obj.Object, []interface{}{
			map[string]interface{}{"conditions": []interface{}{
				map[string]interface{}{"type": "Accepted", "status": "True"},
			}},
		}, "status", "controllers")
		return false, nil, err
	}
	dc.PrependReactor("create", "snippetsfilters", accept)
	dc.PrependReactor("update", "snippetsfilters", accept)
	server, err := common.NewOperatorHTTP()
	require.NoError(t, err)
	op := &hostnameOperator{
		ctx: ctx, ns: "provider", log: log.NewNopLogger(), ac: ac, dc: dc, kc: kfake.NewClientset(),
		hostnames: make(map[string]managedHostname),
		pending:   make(map[hostnameWorkKey]pendingHostname),
		cfg:       common.OperatorConfig{RetryDelay: 20 * time.Millisecond, PruneInterval: time.Hour, WebRefreshInterval: time.Second},
		server:    server, flagHostnamesData: func() {}, flagPendingData: func() {},
		ingressConfig: kube.IngressConfig{IngressMode: builder.IngressModeGateway, GatewayName: "gw", GatewayNamespace: "gateway"},
		gatewayImpl:   gateway.NewNginxGateway(log.NewNopLogger()),
	}
	return op, ac, dc
}

func startRecoveryOperator(t *testing.T, op *hostnameOperator) {
	t.Helper()
	ctx, cancel := context.WithCancel(op.ctx)
	op.ctx = ctx
	done := make(chan error, 1)
	go func() { done <- op.run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(3 * time.Second):
			t.Error("hostname operator did not stop")
		}
	})
}

func recoveryManifest(lid mtypes.LeaseID) *crd.Manifest {
	return &crd.Manifest{
		ObjectMeta: metav1.ObjectMeta{Name: builder.LidNS(lid), Namespace: "provider"},
		Spec: crd.ManifestSpec{Group: crd.ManifestGroup{Services: []crd.ManifestService{{
			Name: "web", Count: 1,
			Expose: []crd.ManifestServiceExpose{{Port: 80, ExternalPort: 80, Global: true}},
		}}}},
	}
}

func seedRecoveryHost(t *testing.T, ac *afake.Clientset, host string, withManifest bool) mtypes.LeaseID {
	t.Helper()
	lid := testutil.LeaseID(t)
	_, err := ac.AkashV2beta2().ProviderHosts("provider").Create(context.Background(), &crd.ProviderHost{
		ObjectMeta: metav1.ObjectMeta{Name: host},
		Spec: crd.ProviderHostSpec{Hostname: host, Owner: lid.Owner, Provider: lid.Provider,
			Dseq: lid.DSeq, Gseq: lid.GSeq, Oseq: lid.OSeq, ServiceName: "web", ExternalPort: 80},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	if withManifest {
		_, err = ac.AkashV2beta2().Manifests("provider").Create(context.Background(), recoveryManifest(lid), metav1.CreateOptions{})
		require.NoError(t, err)
	}
	return lid
}

func recoveryRouteReady(dc *dynamicfake.FakeDynamicClient, lid mtypes.LeaseID, host string) bool {
	route, err := dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lid)).Get(context.Background(), host, metav1.GetOptions{})
	if err != nil {
		return false
	}
	hosts, _, _ := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
	return len(hosts) == 1 && hosts[0] == host
}

func TestHostnameRecoveryIsolatesFailuresAndRetries(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	broken := seedRecoveryHost(t, ac, "a-broken.example", true)
	healthy := seedRecoveryHost(t, ac, "b-healthy.example", true)
	var fail atomic.Bool
	fail.Store(true)
	dc.PrependReactor("create", "snippetsfilters", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if obj.GetName() == "a-broken.example" && fail.Load() {
			return true, nil, kerrors.NewForbidden(recoveryFilterGVR.GroupResource(), obj.GetName(), fmt.Errorf("injected permission failure"))
		}
		return false, nil, nil
	})
	startRecoveryOperator(t, op)
	require.Eventually(t, func() bool { return recoveryRouteReady(dc, healthy, "b-healthy.example") }, 2*time.Second, 10*time.Millisecond,
		"one failed filter must not prevent another deployment from getting its route")
	fail.Store(false)
	require.Eventually(t, func() bool { return recoveryRouteReady(dc, broken, "a-broken.example") }, 2*time.Second, 10*time.Millisecond,
		"the original hostname must recover without a new event or process restart")
}

func TestHostnameRecoveryRetriesMissingManifest(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	lid := seedRecoveryHost(t, ac, "late-manifest.example", false)
	var attempts atomic.Int32
	ac.PrependReactor("get", "manifests", func(ktesting.Action) (bool, runtime.Object, error) {
		attempts.Add(1)
		return false, nil, nil
	})
	startRecoveryOperator(t, op)
	require.Eventually(t, func() bool { return attempts.Load() >= 3 }, time.Second, 10*time.Millisecond)
	_, err := ac.AkashV2beta2().Manifests("provider").Create(context.Background(), recoveryManifest(lid), metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return recoveryRouteReady(dc, lid, "late-manifest.example") }, 2*time.Second, 10*time.Millisecond,
		"a missing dependency must not permanently suppress the lease after three attempts")
}

func TestHostnameRecoveryDeletesPendingRoute(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	const host = "deleted-pending.example"
	lid := seedRecoveryHost(t, ac, host, true)
	var fail atomic.Bool
	fail.Store(true)
	dc.PrependReactor("create", "snippetsfilters", func(ktesting.Action) (bool, runtime.Object, error) {
		if fail.Load() {
			return true, nil, fmt.Errorf("injected filter failure")
		}
		return false, nil, nil
	})
	startRecoveryOperator(t, op)
	routes := dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lid))
	require.Eventually(t, func() bool {
		_, err := routes.Get(context.Background(), host, metav1.GetOptions{})
		return err == nil
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, ac.AkashV2beta2().ProviderHosts("provider").Delete(context.Background(), host, metav1.DeleteOptions{}))
	fail.Store(false)
	require.Eventually(t, func() bool {
		_, err := routes.Get(context.Background(), host, metav1.GetOptions{})
		return kerrors.IsNotFound(err)
	}, time.Second, 10*time.Millisecond)
	require.Never(t, func() bool { return recoveryRouteReady(dc, lid, host) }, 150*time.Millisecond, 10*time.Millisecond,
		"a delayed retry must not recreate a deleted deployment route")
}

func TestHostnameRecoveryRetainsDeleteAcrossWatchRestart(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	const host = "retry-delete.example"
	lid := seedRecoveryHost(t, ac, host, true)
	watches := make(chan watch.Interface, 10)
	ac.PrependWatchReactor("providerhosts", func(action ktesting.Action) (bool, watch.Interface, error) {
		w, err := ac.Tracker().Watch(action.GetResource(), action.GetNamespace())
		if err == nil {
			watches <- w
		}
		return true, w, err
	})
	var fail atomic.Bool
	var deleteAttempts atomic.Int32
	fail.Store(true)
	dc.PrependReactor("delete", "httproutes", func(ktesting.Action) (bool, runtime.Object, error) {
		deleteAttempts.Add(1)
		if fail.Load() {
			return true, nil, fmt.Errorf("injected route delete failure")
		}
		return false, nil, nil
	})
	startRecoveryOperator(t, op)
	require.Eventually(t, func() bool { return recoveryRouteReady(dc, lid, host) }, time.Second, 10*time.Millisecond)
	w := <-watches
	require.NoError(t, ac.AkashV2beta2().ProviderHosts("provider").Delete(context.Background(), host, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return deleteAttempts.Load() > 0 }, time.Second, 10*time.Millisecond)
	w.Stop()
	require.Eventually(t, func() bool { return len(watches) > 0 }, time.Second, 10*time.Millisecond)
	fail.Store(false)
	require.Eventually(t, func() bool {
		_, err := dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(lid)).Get(context.Background(), host, metav1.GetOptions{})
		return kerrors.IsNotFound(err)
	}, time.Second, 10*time.Millisecond, "failed deletion must survive observer relisting")
}

func TestHostnameRecoveryStaleWorkDoesNotReplaceNewLease(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	const host = "moved.example"
	oldLease := seedRecoveryHost(t, ac, host, true)
	oldHost, err := ac.AkashV2beta2().ProviderHosts("provider").Get(context.Background(), host, metav1.GetOptions{})
	require.NoError(t, err)
	oldEvent, err := hostnameEventFromProviderHost(oldHost, ctypes.ProviderResourceAdd)
	require.NoError(t, err)
	require.NoError(t, op.applyAddOrUpdateEvent(context.Background(), oldEvent))

	newLease := testutil.LeaseID(t)
	_, err = ac.AkashV2beta2().Manifests("provider").Create(context.Background(), recoveryManifest(newLease), metav1.CreateOptions{})
	require.NoError(t, err)
	newHost := oldHost.DeepCopy()
	newHost.Spec.Owner, newHost.Spec.Provider = newLease.Owner, newLease.Provider
	newHost.Spec.Dseq, newHost.Spec.Gseq, newHost.Spec.Oseq = newLease.DSeq, newLease.GSeq, newLease.OSeq
	_, err = ac.AkashV2beta2().ProviderHosts("provider").Update(context.Background(), newHost, metav1.UpdateOptions{})
	require.NoError(t, err)
	newEvent, err := hostnameEventFromProviderHost(newHost, ctypes.ProviderResourceUpdate)
	require.NoError(t, err)
	// The old route may already have disappeared while the actor still remembers
	// its connection. That must not prevent publishing the reassigned hostname.
	require.NoError(t, dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(oldLease)).Delete(context.Background(), host, metav1.DeleteOptions{}))
	require.NoError(t, op.applyAddOrUpdateEvent(context.Background(), newEvent))

	// Delayed work for the previous lease must only clean that lease's namespace.
	require.NoError(t, op.reconcileHostname(context.Background(), oldEvent))
	require.True(t, recoveryRouteReady(dc, newLease, host))
	require.Equal(t, newLease, op.hostnames[host].presentLease)
	_, err = dc.Resource(gateway.HTTPRouteGVR).Namespace(builder.LidNS(oldLease)).Get(context.Background(), host, metav1.GetOptions{})
	require.True(t, kerrors.IsNotFound(err))
}

func TestHostnameRecoveryResyncDoesNotStarveSnapshotTail(t *testing.T) {
	op, ac, dc := recoveryOperator(t)
	op.cfg.PruneInterval = 15 * time.Millisecond
	var lastLease mtypes.LeaseID
	var lastHost string
	for i := 0; i < 12; i++ {
		lastHost = fmt.Sprintf("host-%02d.example", i)
		lastLease = seedRecoveryHost(t, ac, lastHost, true)
	}
	// A route attempt takes longer than the refresh interval. Reconnecting the
	// watch on each refresh repeatedly truncates the initial snapshot here.
	dc.PrependReactor("get", "httproutes", func(ktesting.Action) (bool, runtime.Object, error) {
		time.Sleep(10 * time.Millisecond)
		return false, nil, nil
	})
	var watchCount atomic.Int32
	ac.PrependWatchReactor("providerhosts", func(ktesting.Action) (bool, watch.Interface, error) {
		watchCount.Add(1)
		return false, nil, nil
	})
	startRecoveryOperator(t, op)
	require.Eventually(t, func() bool { return recoveryRouteReady(dc, lastLease, lastHost) }, 3*time.Second, 20*time.Millisecond,
		"periodic refresh must deliver the complete snapshot even when API requests are slow")
	require.EqualValues(t, 1, watchCount.Load(), "periodic refresh must preserve the live watch")
}
