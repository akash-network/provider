package hostname

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/testutil"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/logging/logtest"
)

const (
	observeTestNamespace = "lease"
	observeTestTimeout   = 5 * time.Second
)

func testProviderHost(t *testing.T, hostname, owner string) *crd.ProviderHost {
	t.Helper()

	return &crd.ProviderHost{
		ObjectMeta: metav1.ObjectMeta{Name: hostname, Namespace: observeTestNamespace},
		Spec: crd.ProviderHostSpec{
			Owner:        owner,
			Provider:     testutil.AccAddress(t).String(),
			Hostname:     hostname,
			Dseq:         1,
			Gseq:         1,
			Oseq:         1,
			ServiceName:  "web",
			ExternalPort: 80,
		},
	}
}

func receiveEvent(t *testing.T, events <-chan chostname.ResourceEvent) chostname.ResourceEvent {
	t.Helper()

	select {
	case ev, ok := <-events:
		require.True(t, ok, "events closed early")
		return ev
	case <-time.After(observeTestTimeout):
		t.Fatal("no event received")
	}

	return nil
}

// drainUntilClosed returns the events received until events is closed.
func drainUntilClosed(t *testing.T, events <-chan chostname.ResourceEvent) []chostname.ResourceEvent {
	t.Helper()

	var drained []chostname.ResourceEvent

	timeout := time.After(observeTestTimeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return drained
			}
			drained = append(drained, ev)
		case <-timeout:
			t.Fatal("events not closed")
			return nil
		}
	}
}

// A ProviderHost whose lease ID cannot be built (e.g. a hand-edited owner) must be skipped. It
// used to be emitted as an event without a type, which stopped the observation on every retry.
func TestObserveHostnameState_SkipsUnconvertibleProviderHosts(t *testing.T) {
	owners := map[string]string{
		"a.example.com": testutil.AccAddress(t).String(),
		"b.example.com": testutil.AccAddress(t).String(),
		"c.example.com": testutil.AccAddress(t).String(),
	}

	objs := make([]runtime.Object, 0, 1+len(owners))
	objs = append(objs, testProviderHost(t, "bad.example.com", "not-a-bech32-address"))
	for hostname, owner := range owners {
		objs = append(objs, testProviderHost(t, hostname, owner))
	}

	op := &hostnameOperator{ac: akashfake.NewSimpleClientset(objs...), ns: observeTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)

	got := make([]chostname.ResourceEvent, 0, len(owners))
	for range owners {
		got = append(got, receiveEvent(t, events))
	}

	cancel()
	got = append(got, drainUntilClosed(t, events)...)

	hostnames := make([]string, 0, len(got))
	for _, ev := range got {
		require.Equal(t, ctypes.ProviderResourceAdd, ev.GetEventType(), "event %+v", ev)
		require.Equal(t, owners[ev.GetHostname()], ev.GetLeaseID().Owner)
		hostnames = append(hostnames, ev.GetHostname())
	}

	require.ElementsMatch(t, []string{"a.example.com", "b.example.com", "c.example.com"}, hostnames)
}

// The consumer stops reading when applying an event fails. Cancelling the observation must then
// release the producer and stop its watch; the producer used to stay blocked on its send for
// good, keeping the initial list alive, once per observation retry.
func TestObserveHostnameState_CancelReleasesBlockedProducer(t *testing.T) {
	fw := watch.NewFake()

	ac := akashfake.NewSimpleClientset(
		testProviderHost(t, "a.example.com", testutil.AccAddress(t).String()),
		testProviderHost(t, "b.example.com", testutil.AccAddress(t).String()),
	)
	ac.PrependWatchReactor("providerhosts", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})

	op := &hostnameOperator{ac: ac, ns: observeTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := op.observeHostnameState(ctx)
	require.NoError(t, err)

	// the consumer goes away without reading
	cancel()

	require.Eventually(t, fw.IsStopped, observeTestTimeout, 10*time.Millisecond, "producer did not stop the watch")
}

// A watch error event carries a *metav1.Status, not a ProviderHost. It used to panic the
// operator on an unchecked type assertion; it must end the stream so the observation restarts.
func TestObserveHostnameState_WatchErrorEndsStream(t *testing.T) {
	fw := watch.NewFake()

	ac := akashfake.NewSimpleClientset()
	ac.PrependWatchReactor("providerhosts", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})

	op := &hostnameOperator{ac: ac, ns: observeTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := op.observeHostnameState(ctx)
	require.NoError(t, err)

	fw.Error(&metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusGone,
		Reason:  metav1.StatusReasonExpired,
		Message: "too old resource version",
	})

	require.Empty(t, drainUntilClosed(t, events))
	require.Eventually(t, fw.IsStopped, observeTestTimeout, 10*time.Millisecond, "producer did not stop the watch")
}
