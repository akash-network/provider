package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boz/go-lifecycle"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cmocks "github.com/akash-network/provider/mocks/cluster"
	"github.com/akash-network/provider/session"
)

type reclaimTestScaffold struct {
	dm         *deploymentManager
	broadcasts chan []sdk.Msg
	leaseID    mtypes.LeaseID
}

func newReclaimScaffold(t *testing.T, blockTime time.Time, catchingUp bool, broadcastErr error) *reclaimTestScaffold {
	t.Helper()

	providerAddr := testutil.AccAddress(t)
	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(testutil.OrderID(t), providerAddr))

	nodeMocks := &clientmocks.NodeClient{}
	nodeMocks.On("SyncInfo", mock.Anything).Return(&coretypes.SyncInfo{LatestBlockTime: blockTime, CatchingUp: catchingUp}, nil)

	broadcasts := make(chan []sdk.Msg, 4)
	txMocks := &clientmocks.TxClient{}
	txMocks.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		broadcasts <- args.Get(1).([]sdk.Msg)
	}).Return(&sdk.Result{}, broadcastErr)

	clientMocks := &clientmocks.Client{}
	clientMocks.On("Node").Return(nodeMocks)
	clientMocks.On("Tx").Return(txMocks)

	prov := &ptypes.Provider{Owner: providerAddr.String()}
	sess := session.New(testutil.Logger(t), clientMocks, prov, 1)

	return &reclaimTestScaffold{
		dm: &deploymentManager{
			session:    sess,
			deployment: &ctypes.Deployment{Lid: leaseID},
			log:        testutil.Logger(t),
			lc:         lifecycle.New(),
			config:     NewDefaultConfig(),
		},
		broadcasts: broadcasts,
		leaseID:    leaseID,
	}
}

func TestAttemptReclaimCloseBroadcastsWhenElapsed(t *testing.T) {
	s := newReclaimScaffold(t, time.Now(), false, nil)

	deadline := time.Now().Add(-time.Hour).Unix()
	done, wait := s.dm.attemptReclaimClose(context.Background(), deadline)
	require.True(t, done)
	require.Zero(t, wait)

	select {
	case msgs := <-s.broadcasts:
		require.Len(t, msgs, 1)
		msg, ok := msgs[0].(*mvbeta.MsgCloseBid)
		require.True(t, ok)
		require.Equal(t, s.leaseID.BidID(), msg.ID)
		require.Equal(t, mtypes.LeaseClosedReasonDecommissioned, msg.Reason)
	default:
		t.Fatal("expected MsgCloseBid broadcast for an elapsed reclamation deadline")
	}
}

func TestAttemptReclaimCloseGatesOnBlockTimeNotWallClock(t *testing.T) {
	blockTime := time.Now().Add(-2 * time.Hour)
	s := newReclaimScaffold(t, blockTime, false, nil)

	deadline := time.Now().Add(-time.Hour).Unix()
	done, wait := s.dm.attemptReclaimClose(context.Background(), deadline)
	require.False(t, done)
	// Not yet due in block time: retry after the remaining block-time gap, not wall clock.
	require.Equal(t, time.Duration(deadline-blockTime.Unix())*time.Second, wait)
	requireNoBroadcast(t, s.broadcasts)
}

func TestAttemptReclaimCloseSkipsWhileCatchingUp(t *testing.T) {
	s := newReclaimScaffold(t, time.Now(), true, nil)

	deadline := time.Now().Add(-time.Hour).Unix()
	done, wait := s.dm.attemptReclaimClose(context.Background(), deadline)
	require.False(t, done)
	require.Equal(t, s.dm.config.ReclamationCloseRetryInterval, wait)
	requireNoBroadcast(t, s.broadcasts)
}

func TestAttemptReclaimCloseRetriesOnBroadcastError(t *testing.T) {
	s := newReclaimScaffold(t, time.Now(), false, errors.New("rpc down"))

	deadline := time.Now().Add(-time.Hour).Unix()
	done, wait := s.dm.attemptReclaimClose(context.Background(), deadline)
	require.False(t, done)
	require.Equal(t, s.dm.config.ReclamationCloseRetryInterval, wait)
	// The close was attempted; a rejected broadcast retries on the next tick.
	select {
	case <-s.broadcasts:
	default:
		t.Fatal("expected a MsgCloseBid broadcast attempt")
	}
}

// TestReclaimNeverBlocksCaller asserts reclaim() returns promptly even when a
// deadline is already queued and nothing is draining reclaimch. The shared service
// loop calls reclaim() inline, so a blocking send into a manager busy broadcasting a
// close would stall the whole provider's event dispatch. Without the buffered channel
// + non-blocking send this second call would deadlock and the test would time out.
func TestReclaimNeverBlocksCaller(t *testing.T) {
	dm := &deploymentManager{
		reclaimch: make(chan int64, 1),
		lc:        lifecycle.New(),
	}

	// First call buffers the deadline (no loop is running to receive it).
	require.NoError(t, dm.reclaim(1))

	done := make(chan error, 1)
	go func() { done <- dm.reclaim(2) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("reclaim blocked the caller while a deadline was already queued")
	}
}

func requireNoBroadcast(t *testing.T, ch chan []sdk.Msg) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("did not expect a MsgCloseBid broadcast")
	default:
	}
}

// TestReclaimSuppressedByInactiveLeaseTeardown drives the manager down the teardown
// path a lease the chain reports closed takes, then delivers a due reclamation
// deadline into the running loop. That path reaches startTeardown without ever
// touching teardownch, so a suppression keyed off the teardown channel would let the
// close fire and spend fees broadcasting MsgCloseBid for an already closed lease.
func TestReclaimSuppressedByInactiveLeaseTeardown(t *testing.T) {
	lid := testutil.LeaseID(t)
	bus := pubsub.NewBus()
	defer bus.Close()

	client := clientmocks.NewClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	client.On("Query").Return(query)
	query.On("Market").Return(market)
	market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: mtypes.Lease{ID: lid, State: mtypes.LeaseClosed}}, nil).Once()

	node := &clientmocks.NodeClient{}
	node.On("SyncInfo", mock.Anything).Return(&coretypes.SyncInfo{LatestBlockTime: time.Now()}, nil).Maybe()
	broadcasts := make(chan []sdk.Msg, 4)
	tx := &clientmocks.TxClient{}
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		broadcasts <- args.Get(1).([]sdk.Msg)
	}).Return(&sdk.Result{}, nil).Maybe()
	client.On("Node").Return(node).Maybe()
	client.On("Tx").Return(tx).Maybe()

	tearingDown := make(chan struct{})
	releaseTeardown := make(chan struct{})
	kube := cmocks.NewClient(t)
	kube.On("TeardownLease", mock.Anything, lid).Run(func(mock.Arguments) {
		close(tearingDown)
		<-releaseTeardown
	}).Return(nil).Once()
	kube.On("PurgeDeclaredHostnames", mock.Anything, lid).Return(nil).Once()
	kube.On("PurgeDeclaredIPs", mock.Anything, lid).Return(nil).Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostnames, err := newHostnameService(ctx, NewDefaultConfig(), nil)
	require.NoError(t, err)
	s := &service{
		bus: bus, log: testutil.Logger(t), lc: lifecycle.New(),
		client: kube, session: session.New(testutil.Logger(t), client, nil, 0),
		hostnames: hostnames, managerch: make(chan *deploymentManager, 1),
		config: NewDefaultConfig(),
	}

	dm := newDeploymentManager(s, &ctypes.Deployment{Lid: lid, MGroup: &mani.Group{}}, false)
	defer dm.lc.Shutdown(nil)

	select {
	case <-tearingDown:
	case <-time.After(10 * time.Second):
		t.Fatal("closed lease never started teardown")
	}

	require.NoError(t, dm.reclaim(time.Now().Add(-time.Hour).Unix()))
	require.Eventually(t, func() bool { return len(dm.reclaimch) == 0 }, 5*time.Second, 10*time.Millisecond,
		"manager never picked up the reclamation deadline")
	close(releaseTeardown)

	select {
	case <-dm.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("deployment manager never retired")
	}
	require.Equal(t, dsTeardownComplete, dm.state)
	requireNoBroadcast(t, broadcasts)
}

// TestCheckLeaseActiveArmsReclaimDeadline covers the restart path: a lease already
// reclaiming when the provider comes back still owns running workloads, so the
// manager has to re-arm the close it would otherwise never schedule.
func TestCheckLeaseActiveArmsReclaimDeadline(t *testing.T) {
	lid := testutil.LeaseID(t)
	deadline := time.Now().Add(time.Hour).Unix()

	client := clientmocks.NewClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	client.On("Query").Return(query)
	query.On("Market").Return(market)
	market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: mtypes.Lease{
			ID:          lid,
			State:       mtypes.LeaseReclaiming,
			Reclamation: &mtypes.Reclamation{Deadline: deadline},
		}}, nil).Once()

	dm := &deploymentManager{
		session:    session.New(testutil.Logger(t), client, nil, 0),
		log:        testutil.Logger(t),
		deployment: &ctypes.Deployment{Lid: lid},
		reclaimch:  make(chan int64, 1),
		lc:         lifecycle.New(),
	}

	require.NoError(t, dm.checkLeaseActive(context.Background()))
	select {
	case got := <-dm.reclaimch:
		require.Equal(t, deadline, got)
	default:
		t.Fatal("reclaiming lease found on restart did not arm its reclamation close")
	}
}
