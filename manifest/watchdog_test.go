package manifest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tpubsub "github.com/troian/pubsub"

	sdk "github.com/cosmos/cosmos-sdk/types"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/logging/logtest"
)

type watchdogTestScaffold struct {
	client           *clientmocks.Client
	parentCh         chan struct{}
	doneCh           chan dtypes.DeploymentID
	broadcasts       chan []sdk.Msg
	broadcastStarted chan struct{}
	broadcastErr     chan error
	leaseID          mtypes.LeaseID
	provider         ptypes.Provider
}

func makeWatchdogTestScaffold(t *testing.T, timeout time.Duration) (*watchdog, *watchdogTestScaffold) {
	return makeWatchdogTestScaffoldFull(t, timeout, DefaultBroadcastTimeout, nil)
}

func makeWatchdogTestScaffoldWithBlocking(t *testing.T, timeout time.Duration, blockUntilRelease <-chan struct{}) (*watchdog, *watchdogTestScaffold) {
	return makeWatchdogTestScaffoldFull(t, timeout, DefaultBroadcastTimeout, blockUntilRelease)
}

func makeWatchdogTestScaffoldFull(t *testing.T, timeout, broadcastTimeout time.Duration, blockUntilRelease <-chan struct{}) (*watchdog, *watchdogTestScaffold) {
	scaffold := &watchdogTestScaffold{}
	scaffold.parentCh = make(chan struct{})
	scaffold.doneCh = make(chan dtypes.DeploymentID, 1)
	scaffold.provider = testutil.Provider(t)
	scaffold.leaseID = testutil.LeaseID(t)
	scaffold.leaseID.Provider = scaffold.provider.Owner
	scaffold.broadcasts = make(chan []sdk.Msg, 1)
	scaffold.broadcastStarted = make(chan struct{}, 1)
	scaffold.broadcastErr = make(chan error, 1)

	txClientMock := &clientmocks.TxClient{}
	txClientMock.EXPECT().
		BroadcastMsgs(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, msgs []sdk.Msg, _ ...aclient.BroadcastOption) (any, error) {
			select {
			case scaffold.broadcastStarted <- struct{}{}:
			default:
			}

			if blockUntilRelease != nil {
				select {
				case <-blockUntilRelease:
				case <-ctx.Done():
					scaffold.broadcastErr <- ctx.Err()
					return nil, ctx.Err()
				}
			}

			if err := ctx.Err(); err != nil {
				scaffold.broadcastErr <- err
				return nil, err
			}

			scaffold.broadcasts <- msgs
			scaffold.broadcastErr <- nil
			return &sdk.Result{}, nil
		})

	scaffold.client = &clientmocks.Client{}
	scaffold.client.On("Tx").Return(txClientMock)
	sess := session.New(testutil.Logger(t), scaffold.client, &scaffold.provider, -1)

	require.NotNil(t, sess.Client())

	wd := newWatchdog(sess, scaffold.parentCh, scaffold.doneCh, scaffold.leaseID, timeout, broadcastTimeout)

	return wd, scaffold
}

func TestWatchdogTimeout(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 3*time.Second)

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was sent
	broadcasts := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	require.IsType(t, []sdk.Msg{}, broadcasts)

	msgs := broadcasts.([]sdk.Msg)
	require.Len(t, msgs, 1)
	require.IsType(t, &mvbeta.MsgCloseBid{}, msgs[0])

	msg := msgs[0].(*mvbeta.MsgCloseBid)
	require.Equal(t, scaffold.leaseID, msg.ID.LeaseID())

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}

func TestWatchdogStops(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 1*time.Minute)

	wd.stop() // ask it to stop immediately
	wd.stop() // ask it to stop a second time, this is expected usage

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was not sent
	select {
	case <-scaffold.broadcasts:
		t.Fatal("should no have broadcast any message")
	default:
	}

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}

func TestWatchdogStopsOnParent(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 1*time.Minute)

	close(scaffold.parentCh) // ask it to stop immediately

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was not sent
	select {
	case <-scaffold.broadcasts:
		t.Fatal("should no have broadcast any message")
	default:
	}

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}

func TestWatchdogCloseBidIgnoresParentCancelOnceStarted(t *testing.T) {
	releaseCh := make(chan struct{})
	wd, scaffold := makeWatchdogTestScaffoldWithBlocking(t, 100*time.Millisecond, releaseCh)

	select {
	case <-scaffold.broadcastStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for broadcast to start")
	}

	close(scaffold.parentCh)
	close(releaseCh)

	select {
	case err := <-scaffold.broadcastErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for broadcast result")
	}

	select {
	case <-wd.lc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	broadcasts := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	msgs := broadcasts.([]sdk.Msg)
	require.Len(t, msgs, 1)
	require.Equal(t, mtypes.LeaseClosedReasonManifestTimeout, msgs[0].(*mvbeta.MsgCloseBid).Reason)
}

func TestWatchdogBroadcastTimeout(t *testing.T) {
	neverRelease := make(chan struct{})
	wd, scaffold := makeWatchdogTestScaffoldFull(t, 100*time.Millisecond, 10*time.Millisecond, neverRelease)

	select {
	case err := <-scaffold.broadcastErr:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for broadcast timeout")
	}

	select {
	case <-wd.lc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("watchdog hung after broadcast timeout")
	}

	select {
	case <-scaffold.broadcasts:
		t.Fatal("broadcast should not have completed")
	default:
	}
}

// allowedHostnames is a hostname service that lets a tenant reserve any hostname.
type allowedHostnames struct{}

func (allowedHostnames) ReserveHostnames(context.Context, []string, mtypes.LeaseID) ([]string, error) {
	return nil, nil
}

func (allowedHostnames) ReleaseHostnames(mtypes.LeaseID) error { return nil }

func (allowedHostnames) CanReserveHostnames([]string, sdk.Address) error { return nil }

func (allowedHostnames) PrepareHostnamesForTransfer(context.Context, []string, mtypes.LeaseID) error {
	return nil
}

// serviceWatchdogScaffold runs a manifest service for a provider that wins a
// lease for every group of a deployment. The chain reports the leases active.
type serviceWatchdogScaffold struct {
	svc      Service
	bus      pubsub.Bus
	logs     *logtest.Recorder
	manifest maniv2beta2.Manifest
	// won holds the lease of every group of the deployment, in group order.
	won []event.LeaseWon
	// closeBids receives every bid the provider closes.
	closeBids chan *mvbeta.MsgCloseBid
}

func newServiceWatchdogScaffold(t *testing.T, deployment sdl.SDL, manifestTimeout time.Duration) *serviceWatchdogScaffold {
	t.Helper()
	mani, err := deployment.Manifest()
	require.NoError(t, err)
	version, err := mani.Version()
	require.NoError(t, err)
	specs, err := deployment.DeploymentGroups()
	require.NoError(t, err)

	provider := testutil.Provider(t)
	deploymentLease := testutil.LeaseID(t)
	deploymentLease.Provider = provider.Owner
	did := deploymentLease.DeploymentID()
	price := sdk.NewInt64DecCoin("uact", 10)
	groups := make(dvbeta.Groups, 0, len(specs))
	active := make([]mvbeta.QueryLeaseResponse, 0, len(specs))
	var gseq uint32
	for _, spec := range specs {
		gseq++
		lid := deploymentLease
		lid.GSeq = gseq
		groups = append(groups, dvbeta.Group{ID: lid.GroupID(), State: dvbeta.GroupOpen, GroupSpec: spec})
		active = append(active, mvbeta.QueryLeaseResponse{
			Lease: mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: price},
		})
	}
	won := make([]event.LeaseWon, 0, len(groups))
	for i := range groups {
		won = append(won, event.LeaseWon{LeaseID: active[i].Lease.ID, Group: &groups[i], Price: price})
	}

	deployments := deploymentmocks.NewQueryClient(t)
	deployments.On("Deployment", mock.Anything, &dvbeta.QueryDeploymentRequest{ID: did}).Return(
		&dvbeta.QueryDeploymentResponse{
			Deployment: dtypes.Deployment{ID: did, State: dtypes.DeploymentActive, Hash: version},
			Groups:     groups,
		}, nil).Maybe()
	leases := marketmocks.NewQueryClient(t)
	leases.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: did.Owner, DSeq: did.DSeq, Provider: provider.Owner, State: mtypes.LeaseActive.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{Leases: active}, nil).Maybe()
	query := clientmocks.NewQueryClient(t)
	query.On("Deployment").Return(deployments).Maybe()
	query.On("Market").Return(leases).Maybe()

	closeBids := make(chan *mvbeta.MsgCloseBid, 4)
	tx := clientmocks.NewTxClient(t)
	tx.EXPECT().BroadcastMsgs(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, msgs []sdk.Msg, _ ...aclient.BroadcastOption) (any, error) {
			for _, msg := range msgs {
				if closeBid, ok := msg.(*mvbeta.MsgCloseBid); ok {
					closeBids <- closeBid
				}
			}
			return &sdk.Result{}, nil
		}).Maybe()
	client := clientmocks.NewClient(t)
	client.On("Query").Return(query).Maybe()
	client.On("Tx").Return(tx).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	bus := pubsub.NewBus()
	logs := logtest.New()
	svc, err := NewService(ctx, session.New(logs, client, &provider, -1), bus, allowedHostnames{},
		ServiceConfig{ManifestTimeout: manifestTimeout, RPCQueryTimeout: time.Minute, CachedResultMaxAge: time.Minute})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
		cancel()
		bus.Close()
	})

	return &serviceWatchdogScaffold{
		svc:       svc,
		bus:       bus,
		logs:      logs,
		manifest:  mani,
		won:       won,
		closeBids: closeBids,
	}
}

// oneGroupDeployment is a deployment of a single group.
func oneGroupDeployment(t *testing.T) sdl.SDL {
	t.Helper()
	deployment, err := sdl.ReadFile("../testdata/deployment/deployment-v2-nohost.yaml")
	require.NoError(t, err)

	return deployment
}

// twoGroupDeployment is a deployment of two groups, east and west.
func twoGroupDeployment(t *testing.T) sdl.SDL {
	t.Helper()
	deployment, err := sdl.Read([]byte(`---
version: "2.0"

services:
  web:
    image: quay.io/ovrclk/demo-app
    expose:
      - port: 80
        to:
          - global: true
  api:
    image: quay.io/ovrclk/demo-app
    expose:
      - port: 80
        to:
          - global: true

profiles:
  compute:
    web:
      resources:
        cpu:
          units: "0.01"
        memory:
          size: "128Mi"
        storage:
          size: "512Mi"

  placement:
    east:
      pricing:
        web:
          denom: uact
          amount: 10
    west:
      pricing:
        web:
          denom: uact
          amount: 10

deployment:
  web:
    east:
      profile: web
      count: 1
  api:
    west:
      profile: web
      count: 1
`))
	require.NoError(t, err)

	return deployment
}

// submit sends the deployment's manifest, and waits until the manager accepts it.
func (s *serviceWatchdogScaffold) submit(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, s.svc.Submit(ctx, s.won[0].LeaseID.DeploymentID(), s.manifest))
}

// awaitLogged waits until the service has logged msg count times.
func (s *serviceWatchdogScaffold) awaitLogged(t *testing.T, msg string, count int) {
	t.Helper()
	logged := func() int {
		n := 0
		for _, entry := range s.logs.Entries() {
			if entry.Msg == msg {
				n++
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return logged() >= count }, 5*time.Second, time.Millisecond,
		"%q was not logged %d times", msg, count)
}

// awaitManifestReceived waits until the manager hands the lease lid its manifest.
func awaitManifestReceived(t *testing.T, events pubsub.Subscriber, lid mtypes.LeaseID) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events.Events():
			if received, ok := ev.(event.ManifestReceived); ok && received.LeaseID == lid {
				return
			}
		case <-timeout:
			t.Fatalf("lease %v was not handed its manifest", lid)
		}
	}
}

// A lease whose manifest does not arrive within the manifest timeout has its
// bid closed by the watchdog.
func TestServiceClosesBidOfLeaseWithoutManifest(t *testing.T) {
	s := newServiceWatchdogScaffold(t, oneGroupDeployment(t), 100*time.Millisecond)
	lease := s.won[0]

	require.NoError(t, s.bus.Publish(lease))

	select {
	case closed := <-s.closeBids:
		require.Equal(t, lease.LeaseID, closed.ID.LeaseID())
		require.Equal(t, mtypes.LeaseClosedReasonManifestTimeout, closed.Reason)
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog did not close the bid of a lease without a manifest")
	}
}

// The provider can see a lease won after its manifest arrived: the tenant sends
// the manifest as soon as the lease is created on chain. The manager then hands
// the lease that manifest, so no watchdog closes its bid for a missing one.
func TestServiceKeepsBidOfLeaseWonAfterManifest(t *testing.T) {
	const manifestTimeout = 100 * time.Millisecond
	s := newServiceWatchdogScaffold(t, oneGroupDeployment(t), manifestTimeout)
	lease := s.won[0]
	events, err := s.bus.Subscribe()
	require.NoError(t, err)
	defer events.Close()

	s.submit(t)
	awaitManifestReceived(t, events, lease.LeaseID)
	require.NoError(t, s.bus.Publish(lease))
	awaitManifestReceived(t, events, lease.LeaseID)

	select {
	case closed := <-s.closeBids:
		t.Fatalf("the watchdog closed bid %v of a lease that has its manifest", closed.ID)
	case <-time.After(5 * manifestTimeout):
	}
}

// The leases of a deployment's groups can be won at different times. Once the
// manifest has arrived and stopped the watchdog of the first, the provider can
// see another group's lease won. The manager hands that lease the manifest too,
// so no watchdog starts to close its bid for a missing one.
func TestServiceKeepsBidOfGroupWonAfterManifest(t *testing.T) {
	const manifestTimeout = 500 * time.Millisecond
	s := newServiceWatchdogScaffold(t, twoGroupDeployment(t), manifestTimeout)
	require.Len(t, s.won, 2)
	first, later := s.won[0], s.won[1]

	require.NoError(t, s.bus.Publish(first))
	// The service starts a lease's watchdog before it hands the lease to the manager.
	s.awaitLogged(t, "new lease", 1)
	s.submit(t)
	// The manifest stopped the watchdog, and the service dropped it.
	s.awaitLogged(t, "watchdog done", 1)
	require.NoError(t, s.bus.Publish(later))
	s.awaitLogged(t, "new lease", 2)

	select {
	case closed := <-s.closeBids:
		t.Fatalf("the watchdog closed bid %v of a lease that has its manifest", closed.ID)
	case <-time.After(5 * manifestTimeout):
	}
}
