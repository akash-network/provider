package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/avast/retry-go/v4"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	mv1 "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	cmocks "github.com/akash-network/provider/mocks/cluster"
	"github.com/akash-network/provider/session"
)

type reconcileFixture struct {
	provider string
	kube     *cmocks.Client
	market   *marketmocks.QueryClient
	sub      pubsub.Subscriber
	svc      *service
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	provider := testutil.AccAddress(t).String()
	client := clientmocks.NewClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	client.On("Query").Return(query).Maybe()
	query.On("Market").Return(market).Maybe()
	bus := pubsub.NewBus()
	t.Cleanup(bus.Close)
	sub, err := bus.Subscribe()
	require.NoError(t, err)
	t.Cleanup(sub.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hostnames, err := newHostnameService(ctx, Config{}, nil)
	require.NoError(t, err)
	kube := cmocks.NewClient(t)

	return &reconcileFixture{provider: provider, kube: kube, market: market, sub: sub, svc: &service{
		bus: bus, log: log.NewNopLogger(), client: kube, hostnames: hostnames,
		session: session.New(log.NewNopLogger(), client, &ptypes.Provider{Owner: provider}, 0),
	}}
}

// lease returns a lease ID of the fixture's provider.
func (f *reconcileFixture) lease(t *testing.T) mv1.LeaseID {
	lid := testutil.LeaseID(t)
	lid.Provider = f.provider
	return lid
}

// expectActive registers the chain's pages of the provider's active leases.
func (f *reconcileFixture) expectActive(pages ...[]mv1.LeaseID) {
	var key []byte
	for i, page := range pages {
		resp := &mvbeta.QueryLeasesResponse{Pagination: &sdkquery.PageResponse{}}
		for _, lid := range page {
			resp.Leases = append(resp.Leases, mvbeta.QueryLeaseResponse{Lease: mv1.Lease{ID: lid, State: mv1.LeaseActive}})
		}
		var next []byte
		if i < len(pages)-1 {
			next = []byte(fmt.Sprintf("page-%d", i+1))
			resp.Pagination.NextKey = next
		}
		f.market.On("Leases", mock.Anything, f.activePage(key)).Return(resp, nil).Once()
		key = next
	}
}

func (f *reconcileFixture) activePage(key []byte) *mvbeta.QueryLeasesRequest {
	return &mvbeta.QueryLeasesRequest{
		Filters:    mv1.LeaseFilters{Provider: f.provider, State: mv1.LeaseActive.String()},
		Pagination: &sdkquery.PageRequest{Key: key, Limit: leaseReconcilePageLimit},
	}
}

func (f *reconcileFixture) expectLease(lid mv1.LeaseID, state mv1.Lease_State, reason mv1.LeaseClosedReason) {
	f.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: mv1.Lease{ID: lid, State: state, Reason: reason}}, nil).Once()
}

// expectRemoval registers the removal of a closed lease without a deployment
// manager: what it declared in the provider namespace, its namespace and its manifest.
func (f *reconcileFixture) expectRemoval(lid mv1.LeaseID) {
	f.kube.On("PurgeDeclaredHostnames", mock.Anything, lid).Return(nil).Once()
	f.kube.On("PurgeDeclaredIPs", mock.Anything, lid).Return(nil).Once()
	f.kube.On("TeardownLease", mock.Anything, lid).Return(nil).Once()
}

func (f *reconcileFixture) reserve(t *testing.T, hostname string, lid mv1.LeaseID) {
	t.Helper()
	_, err := f.svc.hostnames.ReserveHostnames(context.Background(), []string{hostname}, lid)
	require.NoError(t, err)
}

// requireReservable checks whether another tenant may reserve the hostname.
func (f *reconcileFixture) requireReservable(t *testing.T, hostname string, reservable bool) {
	t.Helper()
	err := f.svc.hostnames.CanReserveHostnames([]string{hostname}, testutil.AccAddress(t))
	if reservable {
		require.NoError(t, err)
	} else {
		require.ErrorIs(t, err, ErrHostnameNotAllowed)
	}
}

// requireReported waits for the lease-closed events a pass published, and
// checks that no others follow.
func (f *reconcileFixture) requireReported(t *testing.T, want ...*mv1.EventLeaseClosed) {
	t.Helper()
	got := make([]*mv1.EventLeaseClosed, 0, len(want))
	timeout := time.After(time.Second)
	for len(got) < len(want) {
		select {
		case ev := <-f.sub.Events():
			if closed, ok := ev.(*mv1.EventLeaseClosed); ok {
				got = append(got, closed)
			}
		case <-timeout:
			t.Fatalf("reported %d closed leases, want %d", len(got), len(want))
		}
	}
	select {
	case ev := <-f.sub.Events():
		t.Fatalf("unexpected event %T %v", ev, ev)
	case <-time.After(50 * time.Millisecond):
	}
	require.ElementsMatch(t, want, got)
}

// requireReturns fails the test unless fn returns promptly.
func requireReturns(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not return")
	}
}

// At startup the provider matches the leases in its cluster against the chain.
// A lease is torn down only once the chain confirms it closed; leases the chain
// does not know, or that still run their workloads, are kept.
func TestReconcileLeasesTearsDownConfirmedClosedLeases(t *testing.T) {
	f := newReconcileFixture(t)
	other, active, reclaiming := f.lease(t), f.lease(t), f.lease(t)
	closedManaged, closedUnmanaged, overdrawnUnmanaged := f.lease(t), f.lease(t), f.lease(t)
	unknown, unknownStatus, reactivated := f.lease(t), f.lease(t), f.lease(t)
	foreign := testutil.LeaseID(t)
	// The provider also won the order the overdrawn lease's deployment group placed next.
	successor := overdrawnUnmanaged
	successor.OSeq++

	f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{
		active, reclaiming, closedManaged, closedUnmanaged, overdrawnUnmanaged,
		unknown, unknownStatus, reactivated, foreign,
	}, nil).Once()
	// The active leases span two pages, so the whole list must be read.
	f.expectActive([]mv1.LeaseID{other, successor}, []mv1.LeaseID{active})
	// A lease in reclamation still runs its workloads.
	f.expectLease(reclaiming, mv1.LeaseReclaiming, mv1.LeaseClosedReasonInvalid)
	f.expectLease(closedManaged, mv1.LeaseClosed, mv1.LeaseClosedReasonOwner)
	f.expectLease(closedUnmanaged, mv1.LeaseClosed, mv1.LeaseClosedReasonUnspecified)
	f.expectLease(overdrawnUnmanaged, mv1.LeaseInsufficientFunds, mv1.LeaseClosedReasonInsufficientFunds)
	f.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: unknown}).Return(nil, mv1.ErrLeaseNotFound).Once()
	f.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: unknownStatus}).Return(
		nil, status.Error(codes.NotFound, "lease not found")).Once()
	f.expectLease(reactivated, mv1.LeaseActive, mv1.LeaseClosedReasonInvalid)
	// Without a deployment manager, nothing else removes what they hold in the cluster.
	f.expectRemoval(closedUnmanaged)
	f.expectRemoval(overdrawnUnmanaged)
	f.reserve(t, "closed.example.com", closedUnmanaged)
	f.reserve(t, "overdrawn.example.com", overdrawnUnmanaged)

	managed := map[mv1.LeaseID]bool{active: true, closedManaged: true}
	require.NoError(t, f.svc.reconcileLeasesOnce(context.Background(), managed))
	// A lease with a deployment manager is torn down by its manager.
	f.requireReported(t, &mv1.EventLeaseClosed{ID: closedManaged, Reason: mv1.LeaseClosedReasonOwner})
	// Hostnames are reserved per deployment group, so the successor keeps the group's.
	f.requireReservable(t, "closed.example.com", true)
	f.requireReservable(t, "overdrawn.example.com", false)
}

// A lease's namespace and manifest go last: while what it declared in the
// provider namespace cannot be purged, they stay, so the next pass finds the
// lease again, and its hostnames stay reserved.
func TestReconcileLeasesRemovesNamespaceAndManifestLast(t *testing.T) {
	purgeErr := errors.New("kube API unavailable")
	for _, tc := range []struct {
		name           string
		hostnames, ips error
	}{
		{name: "hostnames not purged", hostnames: purgeErr},
		{name: "leased IPs not purged", ips: purgeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReconcileFixture(t)
			closed := f.lease(t)
			f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{closed}, nil).Once()
			f.expectActive(nil)
			f.expectLease(closed, mv1.LeaseClosed, mv1.LeaseClosedReasonOwner)
			f.kube.On("PurgeDeclaredHostnames", mock.Anything, closed).Return(tc.hostnames).Once()
			f.kube.On("PurgeDeclaredIPs", mock.Anything, closed).Return(tc.ips).Once()
			f.reserve(t, "closed.example.com", closed)

			require.ErrorIs(t, f.svc.reconcileLeasesOnce(context.Background(), nil), purgeErr)
			f.requireReservable(t, "closed.example.com", false)
			f.requireReported(t)
		})
	}
}

// When the cluster or the chain cannot be read, nothing is torn down and the
// pass fails, so it is retried.
func TestReconcileLeasesKeepsLeasesWhenListingFails(t *testing.T) {
	listErr := errors.New("RPC unavailable")
	for _, tc := range []struct {
		name   string
		expect func(f *reconcileFixture, lid mv1.LeaseID)
		want   error
	}{
		{name: "cluster listing fails", want: listErr, expect: func(f *reconcileFixture, _ mv1.LeaseID) {
			f.kube.On("LeaseIDs", mock.Anything).Return(nil, listErr).Once()
		}},
		{name: "active lease listing fails", want: listErr, expect: func(f *reconcileFixture, lid mv1.LeaseID) {
			f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{lid}, nil).Once()
			f.market.On("Leases", mock.Anything, f.activePage(nil)).Return(nil, listErr).Once()
		}},
		{name: "active lease pagination stalls", want: errLeasePaginationStalled, expect: func(f *reconcileFixture, lid mv1.LeaseID) {
			f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{lid}, nil).Once()
			stalled := &mvbeta.QueryLeasesResponse{Pagination: &sdkquery.PageResponse{NextKey: []byte("next")}}
			f.market.On("Leases", mock.Anything, f.activePage(nil)).Return(stalled, nil).Once()
			f.market.On("Leases", mock.Anything, f.activePage([]byte("next"))).Return(stalled, nil).Once()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReconcileFixture(t)
			tc.expect(f, f.lease(t))
			require.ErrorIs(t, f.svc.reconcileLeasesOnce(context.Background(), nil), tc.want)
			f.requireReported(t)
		})
	}
}

// A lease whose state cannot be read is kept, while the others are still
// reconciled; the pass fails so the kept lease is checked again.
func TestReconcileLeasesRetriesUnconfirmedLeases(t *testing.T) {
	f := newReconcileFixture(t)
	unreadable, closed := f.lease(t), f.lease(t)
	queryErr := errors.New("RPC unavailable")
	f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{unreadable, closed}, nil).Once()
	f.expectActive(nil)
	f.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: unreadable}).Return(nil, queryErr).Once()
	f.expectLease(closed, mv1.LeaseClosed, mv1.LeaseClosedReasonOwner)
	f.expectRemoval(closed)

	require.ErrorIs(t, f.svc.reconcileLeasesOnce(context.Background(), nil), queryErr)
	f.requireReported(t)
}

// A failed pass is retried, for as long as it takes, until one completes.
func TestReconcileLeasesRetriesUntilAPassCompletes(t *testing.T) {
	f := newReconcileFixture(t)
	f.kube.On("LeaseIDs", mock.Anything).Return(nil, errors.New("kube API unavailable")).Times(20)
	f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID(nil), nil).Once()

	requireReturns(t, func() {
		f.svc.reconcileLeases(context.Background(), nil, retry.Delay(time.Millisecond), retry.MaxDelay(time.Millisecond))
	})
}

// Stopping the service stops the reconciliation, even while it waits to retry.
func TestReconcileLeasesStopsWithTheService(t *testing.T) {
	f := newReconcileFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.kube.On("LeaseIDs", mock.Anything).Return(nil, errors.New("kube API unavailable")).Run(func(mock.Arguments) {
		cancel()
	}).Once()

	requireReturns(t, func() { f.svc.reconcileLeases(ctx, nil) })
}

// Startup resumes the deployments found in the cluster without waiting for the
// chain: the lease reconciliation runs in the background.
func TestStartDeploymentManagersReconcilesInBackground(t *testing.T) {
	f := newReconcileFixture(t)
	listing, release := make(chan struct{}), make(chan struct{})
	f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID(nil), nil).Run(func(mock.Arguments) {
		close(listing)
		<-release
	}).Once()
	f.svc.managers = map[mv1.LeaseID]*deploymentManager{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	go func() {
		f.svc.startDeploymentManagers(ctx, nil)
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup waited for the lease reconciliation")
	}
	select {
	case <-listing:
	case <-time.After(time.Second):
		t.Fatal("startup did not reconcile the leases in the cluster")
	}
	close(release)
}

// Without leases of its own in the cluster, the provider does not query the chain.
func TestReconcileLeasesSkipsChainWithoutLeases(t *testing.T) {
	f := newReconcileFixture(t)
	f.kube.On("LeaseIDs", mock.Anything).Return([]mv1.LeaseID{testutil.LeaseID(t)}, nil).Once()

	require.NoError(t, f.svc.reconcileLeasesOnce(context.Background(), nil))
	f.requireReported(t)
}
