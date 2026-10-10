package hostname

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ktesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/testutil"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
)

func observerTestProviderHost(t *testing.T, name string) crd.ProviderHost {
	t.Helper()
	lease := testutil.LeaseID(t)
	return crd.ProviderHost{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "provider"},
		Spec: crd.ProviderHostSpec{
			Owner: lease.Owner, Provider: lease.Provider,
			Dseq: lease.DSeq, Gseq: lease.GSeq, Oseq: lease.OSeq,
			Hostname: name, ServiceName: "web", ExternalPort: 80,
		},
	}
}

func observerTestOperator(t *testing.T, hosts ...crd.ProviderHost) (*hostnameOperator, *afake.Clientset, *watch.RaceFreeFakeWatcher) {
	t.Helper()
	ac := afake.NewClientset()
	ac.PrependReactor("list", "providerhosts", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &crd.ProviderHostList{ListMeta: metav1.ListMeta{ResourceVersion: "42"}, Items: hosts}, nil
	})
	watcher := watch.NewRaceFreeFake()
	t.Cleanup(watcher.Stop)
	ac.PrependWatchReactor("providerhosts", func(ktesting.Action) (bool, watch.Interface, error) {
		return true, watcher, nil
	})
	return &hostnameOperator{ac: ac, ns: "provider", log: testutil.Logger(t)}, ac, watcher
}

func observerReadEvent(t *testing.T, events <-chan chostname.ResourceEvent) chostname.ResourceEvent {
	t.Helper()
	select {
	case ev, ok := <-events:
		require.True(t, ok, "observer closed before expected event")
		return ev
	case <-time.After(time.Second):
		t.Fatal("observer did not deliver an event")
		return nil
	}
}

func TestObserveHostnameStateWatchesFromSnapshotResourceVersion(t *testing.T) {
	host := observerTestProviderHost(t, "snapshot.example")
	op, ac, watcher := observerTestOperator(t, host)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	ev := observerReadEvent(t, events)
	require.Equal(t, host.Spec.Hostname, ev.GetHostname())
	require.Equal(t, host.Spec.Owner, ev.GetLeaseID().Owner)
	require.Equal(t, ctypes.ProviderResourceAdd, ev.GetEventType())

	var watchedVersion string
	for _, action := range ac.Actions() {
		if action, ok := action.(ktesting.WatchAction); ok {
			watchedVersion = action.GetWatchRestrictions().ResourceVersion
		}
	}
	require.Equal(t, "42", watchedVersion, "watch must include changes made after the list snapshot")

	host.Spec.ServiceName = "updated"
	watcher.Modify(&host)
	ev = observerReadEvent(t, events)
	require.Equal(t, ctypes.ProviderResourceUpdate, ev.GetEventType())
	require.Equal(t, "updated", ev.GetServiceName())
}

func TestObserveHostnameStateCancellationReleasesBlockedReplay(t *testing.T) {
	op, _, watcher := observerTestOperator(t, observerTestProviderHost(t, "blocked.example"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	cancel()
	// Do not consume the initial snapshot. Cancellation must release its blocked send.
	require.Eventually(t, watcher.IsStopped, time.Second, time.Millisecond)
	_, ok := <-events
	require.False(t, ok)
}

func TestObserveHostnameStateCancellationReleasesBlockedWatchSend(t *testing.T) {
	op, _, watcher := observerTestOperator(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	host := observerTestProviderHost(t, "blocked-watch.example")
	watcher.Add(&host)
	// Wait until the observer has consumed the watch event and must send it onward.
	require.Eventually(t, func() bool { return len(watcher.ResultChan()) == 0 }, time.Second, time.Millisecond)
	cancel()
	require.Eventually(t, watcher.IsStopped, time.Second, time.Millisecond)
	_, ok := <-events
	require.False(t, ok)
}

func TestObserveHostnameStateIsolatesInvalidResources(t *testing.T) {
	invalid := observerTestProviderHost(t, "invalid.example")
	invalid.Spec.Owner = "not-an-address"
	valid := observerTestProviderHost(t, "healthy.example")
	op, _, watcher := observerTestOperator(t, invalid, valid)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	require.Equal(t, valid.Spec.Hostname, observerReadEvent(t, events).GetHostname())

	watcher.Action(watch.Bookmark, &metav1.PartialObjectMetadata{})
	watcher.Add(&metav1.Status{})
	watcher.Add(&invalid)
	invalid.Spec.Owner = valid.Spec.Owner
	invalid.Spec.Provider = "not-an-address"
	watcher.Add(&invalid)
	watcher.Delete(&valid)
	ev := observerReadEvent(t, events)
	require.Equal(t, valid.Spec.Hostname, ev.GetHostname())
	require.Equal(t, ctypes.ProviderResourceDelete, ev.GetEventType())
}

func TestObserveHostnameStateWatchErrorClosesStream(t *testing.T) {
	op, _, watcher := observerTestOperator(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	watcher.Error(&metav1.Status{Reason: metav1.StatusReasonExpired, Code: 410})
	select {
	case _, ok := <-events:
		require.False(t, ok, "watch errors must force a fresh list")
	case <-time.After(time.Second):
		t.Fatal("watch error did not close event stream")
	}
	require.True(t, watcher.IsStopped())
}

func TestObserveHostnameStateIncludesAllSnapshotPages(t *testing.T) {
	first := observerTestProviderHost(t, "first.example")
	second := observerTestProviderHost(t, "second.example")
	op, ac, _ := observerTestOperator(t)
	ac.PrependReactor("list", "providerhosts", func(action ktesting.Action) (bool, runtime.Object, error) {
		opts := action.(ktesting.ListActionImpl).GetListOptions()
		if opts.Continue == "" {
			return true, &crd.ProviderHostList{
				ListMeta: metav1.ListMeta{ResourceVersion: "42", Continue: "next"},
				Items:    []crd.ProviderHost{first},
			}, nil
		}
		return true, &crd.ProviderHostList{
			ListMeta: metav1.ListMeta{ResourceVersion: "42"},
			Items:    []crd.ProviderHost{second},
		}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)
	require.Equal(t, first.Spec.Hostname, observerReadEvent(t, events).GetHostname())
	require.Equal(t, second.Spec.Hostname, observerReadEvent(t, events).GetHostname())

	var continuations []string
	for _, action := range ac.Actions() {
		if action, ok := action.(ktesting.ListActionImpl); ok {
			continuations = append(continuations, action.GetListOptions().Continue)
		}
		if action, ok := action.(ktesting.WatchAction); ok {
			require.Equal(t, "42", action.GetWatchRestrictions().ResourceVersion)
		}
	}
	require.Equal(t, []string{"", "next"}, continuations)
}
