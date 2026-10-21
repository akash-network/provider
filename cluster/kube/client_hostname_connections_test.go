package kube

import (
	"context"
	"net/http"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/logging/logtest"
)

const (
	hostnameTestNamespace = "lease"
	hostnameTestTimeout   = 5 * time.Second
)

func testManagedProviderHost(t *testing.T, hostname, owner string) *crd.ProviderHost {
	t.Helper()

	return &crd.ProviderHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hostname,
			Namespace: hostnameTestNamespace,
			Labels:    map[string]string{builder.AkashManagedLabelName: "true"},
		},
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

func recvHostnameEvent(t *testing.T, events <-chan chostname.ResourceEvent) chostname.ResourceEvent {
	t.Helper()

	select {
	case ev, ok := <-events:
		require.True(t, ok, "events closed early")
		return ev
	case <-time.After(hostnameTestTimeout):
		t.Fatal("no event received")
	}

	return nil
}

// drainHostnameEvents returns the events received until events is closed.
func drainHostnameEvents(t *testing.T, events <-chan chostname.ResourceEvent) []chostname.ResourceEvent {
	t.Helper()

	var drained []chostname.ResourceEvent

	timeout := time.After(hostnameTestTimeout)
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

// Hostname reservations compare owner, dseq and gseq only. A ProviderHost whose spec does not
// yield a valid lease ID must neither abort provider startup (cluster.NewService fails on any
// AllHostnames error) nor drop its reservation, which would let another tenant claim the
// hostname: it stays reserved for the owner that can still be recovered, or for no tenant.
func TestAllHostnames_KeepsDamagedProviderHostsReserved(t *testing.T) {
	specOwner := testutil.AccAddress(t).String()
	labelOwner := testutil.AccAddress(t).String()
	noTenant := sdk.AccAddress(make([]byte, 20)).String()

	tests := map[string]struct {
		damage    func(ph *crd.ProviderHost)
		wantOwner string
	}{
		"damaged provider keeps the spec owner": {
			damage:    func(ph *crd.ProviderHost) { ph.Spec.Provider = "not-a-bech32-address" },
			wantOwner: specOwner,
		},
		"damaged owner falls back to the owner label": {
			damage: func(ph *crd.ProviderHost) {
				ph.Spec.Owner = "not-a-bech32-address"
				ph.Labels[builder.AkashLeaseOwnerLabelName] = labelOwner
			},
			wantOwner: labelOwner,
		},
		"unrecoverable owner reserves the hostname for no tenant": {
			damage:    func(ph *crd.ProviderHost) { ph.Spec.Owner = "not-a-bech32-address" },
			wantOwner: noTenant,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			damaged := testManagedProviderHost(t, "damaged.example.com", specOwner)
			tc.damage(damaged)

			goodOwner := testutil.AccAddress(t).String()

			c := &client{
				ac:  akashfake.NewSimpleClientset(damaged, testManagedProviderHost(t, "good.example.com", goodOwner)),
				ns:  hostnameTestNamespace,
				log: logtest.New(),
			}

			hostnames, err := c.AllHostnames(context.Background())
			require.NoError(t, err)

			owners := make(map[string]string, len(hostnames))
			for _, h := range hostnames {
				// reservations parse the owner at startup; an invalid one would abort it
				_, err := h.ID.DeploymentID().GetOwnerAddress()
				require.NoError(t, err, "owner of %s", h.Hostname)

				owners[h.Hostname] = h.ID.Owner
			}

			require.Equal(t, map[string]string{
				"damaged.example.com": tc.wantOwner,
				"good.example.com":    goodOwner,
			}, owners)
		})
	}
}

// A resource whose lease ID cannot be built must be skipped rather than emitted without a type.
func TestClientObserveHostnameState_SkipsUnconvertibleProviderHosts(t *testing.T) {
	owners := map[string]string{
		"a.example.com": testutil.AccAddress(t).String(),
		"b.example.com": testutil.AccAddress(t).String(),
		"c.example.com": testutil.AccAddress(t).String(),
	}

	objs := make([]runtime.Object, 0, 1+len(owners))
	objs = append(objs, testManagedProviderHost(t, "bad.example.com", "not-a-bech32-address"))
	for hostname, owner := range owners {
		objs = append(objs, testManagedProviderHost(t, hostname, owner))
	}

	c := &client{ac: akashfake.NewSimpleClientset(objs...), ns: hostnameTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := c.ObserveHostnameState(ctx)
	require.NoError(t, err)

	got := make([]chostname.ResourceEvent, 0, len(owners))
	for range owners {
		got = append(got, recvHostnameEvent(t, events))
	}

	cancel()
	got = append(got, drainHostnameEvents(t, events)...)

	hostnames := make([]string, 0, len(got))
	for _, ev := range got {
		require.Equal(t, ctypes.ProviderResourceAdd, ev.GetEventType(), "event %+v", ev)
		require.Equal(t, owners[ev.GetHostname()], ev.GetLeaseID().Owner)
		hostnames = append(hostnames, ev.GetHostname())
	}

	require.ElementsMatch(t, []string{"a.example.com", "b.example.com", "c.example.com"}, hostnames)
}

// Cancelling the observation must release a producer whose consumer stopped reading, and stop
// its watch.
func TestClientObserveHostnameState_CancelReleasesBlockedProducer(t *testing.T) {
	fw := watch.NewFake()

	ac := akashfake.NewSimpleClientset(
		testManagedProviderHost(t, "a.example.com", testutil.AccAddress(t).String()),
		testManagedProviderHost(t, "b.example.com", testutil.AccAddress(t).String()),
	)
	ac.PrependWatchReactor("providerhosts", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})

	c := &client{ac: ac, ns: hostnameTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := c.ObserveHostnameState(ctx)
	require.NoError(t, err)

	// the consumer goes away without reading
	cancel()

	require.Eventually(t, fw.IsStopped, hostnameTestTimeout, 10*time.Millisecond, "producer did not stop the watch")
}

// A watch error event carries a *metav1.Status, not a ProviderHost. It must end the stream
// instead of panicking on the type assertion.
func TestClientObserveHostnameState_WatchErrorEndsStream(t *testing.T) {
	fw := watch.NewFake()

	ac := akashfake.NewSimpleClientset()
	ac.PrependWatchReactor("providerhosts", func(k8stesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})

	c := &client{ac: ac, ns: hostnameTestNamespace, log: logtest.New()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := c.ObserveHostnameState(ctx)
	require.NoError(t, err)

	fw.Error(&metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    http.StatusGone,
		Reason:  metav1.StatusReasonExpired,
		Message: "too old resource version",
	})

	require.Empty(t, drainHostnameEvents(t, events))
	require.Eventually(t, fw.IsStopped, hostnameTestTimeout, 10*time.Millisecond, "producer did not stop the watch")
}
