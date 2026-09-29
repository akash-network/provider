package cluster

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	mv1 "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	cmocks "github.com/akash-network/provider/mocks/cluster"
	"github.com/akash-network/provider/session"
)

func TestDeploymentManagerCleansUpClosedLeaseOnRecovery(t *testing.T) {
	lid := testutil.LeaseID(t)
	bus := pubsub.NewBus()
	defer bus.Close()
	sub, err := bus.Subscribe()
	require.NoError(t, err)
	defer sub.Close()
	client := clientmocks.NewClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	client.On("Query").Return(query)
	query.On("Market").Return(market)
	market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: mv1.Lease{ID: lid, State: mv1.LeaseClosed}}, nil).Once()
	kube := cmocks.NewClient(t)
	kube.On("TeardownLease", mock.Anything, lid).Return(nil).Once()
	kube.On("PurgeDeclaredHostnames", mock.Anything, lid).Return(nil).Once()
	kube.On("PurgeDeclaredIPs", mock.Anything, lid).Return(nil).Once()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostnames, err := newHostnameService(ctx, NewDefaultConfig(), nil)
	require.NoError(t, err)
	s := &service{
		bus: bus, log: log.NewNopLogger(), lc: lifecycle.New(),
		client: kube, session: session.New(log.NewNopLogger(), client, nil, 0),
		hostnames: hostnames, managerch: make(chan *deploymentManager, 1),
		config: NewDefaultConfig(),
	}
	dm := newDeploymentManager(s, &ctypes.Deployment{Lid: lid, MGroup: &mani.Group{}}, false)
	defer dm.lc.Shutdown(nil)
	select {
	case <-dm.lc.Done():
	case <-time.After(time.Second):
		t.Fatal("recovered closed lease kept its deployment manager instead of tearing down")
	}
	kube.AssertNotCalled(t, "Deploy", mock.Anything, mock.Anything)
	require.Equal(t, dsTeardownComplete, dm.state)
	// Startup cleanup must also retire the withdrawal monitor it registered.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-sub.Events():
			if removed, ok := ev.(event.LeaseRemoveFundsMonitor); ok {
				require.Equal(t, lid, removed.LeaseID)
				return
			}
		case <-deadline.C:
			t.Fatal("startup cleanup left its withdrawal monitor registered")
		}
	}
}

func TestDeploymentManagerDoesNotTreatMissingLeaseAsClosed(t *testing.T) {
	client := clientmocks.NewClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	client.On("Query").Return(query)
	query.On("Market").Return(market)
	market.On("Lease", mock.Anything, mock.Anything).Return(nil, mv1.ErrLeaseNotFound).Once()
	dm := &deploymentManager{
		session: session.New(log.NewNopLogger(), client, nil, 0), log: log.NewNopLogger(),
		deployment: &ctypes.Deployment{Lid: testutil.LeaseID(t)},
	}
	err := dm.checkLeaseActive(context.Background())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLeaseInactive)
}

func TestDeploymentManagerLeaseState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   mv1.Lease_State
		wrongID bool
		closed  bool
		wantErr bool
	}{
		{name: "active", state: mv1.LeaseActive},
		{name: "reclaiming", state: mv1.LeaseReclaiming},
		{name: "closed", state: mv1.LeaseClosed, closed: true, wantErr: true},
		{name: "insufficient funds", state: mv1.LeaseInsufficientFunds, closed: true, wantErr: true},
		{name: "unknown", state: mv1.LeaseStateInvalid, wantErr: true},
		{name: "wrong lease", state: mv1.LeaseClosed, wrongID: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			lease := mv1.Lease{ID: lid, State: tc.state}
			if tc.wrongID {
				lease.ID.DSeq++
			}
			client := clientmocks.NewClient(t)
			query := clientmocks.NewQueryClient(t)
			market := marketmocks.NewQueryClient(t)
			client.On("Query").Return(query)
			query.On("Market").Return(market)
			market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
				&mvbeta.QueryLeaseResponse{Lease: lease}, nil).Once()
			dm := &deploymentManager{
				session: session.New(log.NewNopLogger(), client, nil, 0), log: log.NewNopLogger(),
				deployment: &ctypes.Deployment{Lid: lid},
			}
			err := dm.checkLeaseActive(context.Background())
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.closed {
				require.ErrorIs(t, err, ErrLeaseInactive)
			} else {
				require.NotErrorIs(t, err, ErrLeaseInactive)
			}
		})
	}
}
