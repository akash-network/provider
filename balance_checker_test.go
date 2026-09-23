package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	dv1 "pkg.akt.dev/go/node/deployment/v1"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/util/pubsub"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

// balanceCheckerTestHarness wires a real balanceChecker and a real pubsub bus to a chain
// query client whose deployment reports the given state.
type balanceCheckerTestHarness struct {
	bus pubsub.Bus
	sub pubsub.Subscriber
	bc  *balanceChecker
}

func newBalanceCheckerTestHarness(t *testing.T, depState dv1.Deployment_State) *balanceCheckerTestHarness {
	t.Helper()

	depQ := &deploymentmocks.QueryClient{}
	depQ.EXPECT().Deployment(mock.Anything, mock.Anything).
		Return(&dtypes.QueryDeploymentResponse{
			Deployment: dv1.Deployment{State: depState},
		}, nil)

	// Only reached for a non-closed deployment; return an error so the check short-circuits
	// before the funds math instead of exercising the withdrawal path.
	marketQ := &marketmocks.QueryClient{}
	marketQ.EXPECT().Leases(mock.Anything, mock.Anything).Return(nil, errors.New("leases query not stubbed")).Maybe()

	aqc := &clientmocks.QueryClient{}
	aqc.EXPECT().Deployment().Return(depQ)
	aqc.EXPECT().Market().Return(marketQ).Maybe()

	// A synced node, so the balance-checker trusts the query result.
	node := &clientmocks.NodeClient{}
	node.EXPECT().SyncInfo(mock.Anything).
		Return(&coretypes.SyncInfo{LatestBlockHeight: 100, CatchingUp: false}, nil)

	cl := &clientmocks.Client{}
	cl.EXPECT().Node().Return(node)

	bus := pubsub.NewBus()
	sub, err := bus.Subscribe()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sess := session.New(log.NewNopLogger(), cl, &ptypes.Provider{}, 0)
	bc, err := newBalanceChecker(ctx, aqc, sdk.AccAddress("provider"), sess, bus, BalanceCheckerConfig{
		LeaseFundsCheckInterval: time.Minute,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = bc.Close()
		bus.Close()
	})

	return &balanceCheckerTestHarness{bus: bus, sub: sub, bc: bc}
}

// When the chain reports the deployment closed, the balance-checker publishes EventLeaseClosed
// so the normal teardown path runs even though the real chain event was never delivered.
func TestBalanceCheckerPublishesLeaseClosedWhenDeploymentClosed(t *testing.T) {
	h := newBalanceCheckerTestHarness(t, dv1.DeploymentClosed)
	lid := mtypes.LeaseID{Owner: "akash1owner", DSeq: 100, GSeq: 1, OSeq: 1, Provider: "akash1provider"}

	// IsNewLease triggers an immediate escrow check.
	require.NoError(t, h.bus.Publish(event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-h.sub.Events():
			if closed, ok := ev.(*mtypes.EventLeaseClosed); ok {
				require.Equal(t, lid, closed.ID)
				return
			}
		case <-deadline:
			t.Fatal("expected balance-checker to publish EventLeaseClosed for a closed deployment")
		}
	}
}

// A deployment that is still active must not produce a close event.
func TestBalanceCheckerNoLeaseClosedWhenDeploymentActive(t *testing.T) {
	h := newBalanceCheckerTestHarness(t, dv1.DeploymentActive)
	lid := mtypes.LeaseID{Owner: "akash1owner", DSeq: 100, GSeq: 1, OSeq: 1, Provider: "akash1provider"}

	require.NoError(t, h.bus.Publish(event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}))

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case ev := <-h.sub.Events():
			_, ok := ev.(*mtypes.EventLeaseClosed)
			require.False(t, ok, "no EventLeaseClosed expected for an active deployment")
		case <-deadline:
			return
		}
	}
}
