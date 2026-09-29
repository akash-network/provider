package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	"github.com/boz/go-lifecycle"
	tmrpc "github.com/cometbft/cometbft/rpc/core/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	etypes "pkg.akt.dev/go/node/escrow/types/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

type balanceTestSubscriber struct {
	pubsub.Subscriber
	events chan pubsub.Event
}

func TestBalanceCheckerPreservesNonterminalAndUncertainLeases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      mtypes.Lease_State
		queryErr   error
		catchingUp bool
		wrongID    bool
		wantErr    bool
	}{
		{name: "active", state: mtypes.LeaseActive},
		{name: "reclaiming", state: mtypes.LeaseReclaiming},
		{name: "unknown state", state: mtypes.LeaseStateInvalid, wantErr: true},
		{name: "RPC error", queryErr: errors.New("RPC unavailable"), wantErr: true},
		{name: "missing lease", queryErr: mtypes.ErrLeaseNotFound, wantErr: true},
		{name: "node catching up", catchingUp: true, wantErr: true},
		{name: "wrong lease", state: mtypes.LeaseClosed, wrongID: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			client := clientmocks.NewClient(t)
			node := clientmocks.NewNodeClient(t)
			query := clientmocks.NewQueryClient(t)
			market := marketmocks.NewQueryClient(t)
			client.On("Node").Return(node)
			node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{CatchingUp: tc.catchingUp}, nil)
			if !tc.catchingUp {
				query.On("Market").Return(market)
				lease := mtypes.Lease{ID: lid, State: tc.state}
				if tc.wrongID {
					lease.ID.DSeq++
				}
				market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
					&mvbeta.QueryLeaseResponse{Lease: lease}, tc.queryErr).Once()
			}
			if tc.state == mtypes.LeaseActive {
				deployment := deploymentmocks.NewQueryClient(t)
				query.On("Deployment").Return(deployment)
				deployment.On("Deployment", mock.Anything, mock.Anything).Return(fundedDeployment(), nil)
				market.On("Leases", mock.Anything, mock.Anything).Return(&mvbeta.QueryLeasesResponse{
					Leases: []mvbeta.QueryLeaseResponse{{Lease: mtypes.Lease{
						ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1)),
					}}},
				}, nil)
			}
			bc := &balanceChecker{
				aqc: query, session: session.New(log.NewNopLogger(), client, nil, 0),
				cfg: BalanceCheckerConfig{LeaseFundsCheckInterval: time.Minute},
			}
			res := bc.doEscrowCheck(context.Background(), lid, true)
			require.Nil(t, res.closed)
			if tc.wantErr {
				require.Error(t, res.err)
			} else {
				require.NoError(t, res.err)
				require.Equal(t, respState(respStateScheduledWithdraw), res.state)
				require.Positive(t, res.checkAfter)
			}
			client.AssertNotCalled(t, "Tx")
		})
	}
}

func fundedDeployment() *dtypes.QueryDeploymentResponse {
	return &dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
		Funds: []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1000000)}},
	}}}
}

func TestBalanceCheckerReconcilesClosedLease(t *testing.T) {
	for _, state := range []mtypes.Lease_State{mtypes.LeaseClosed, mtypes.LeaseInsufficientFunds} {
		t.Run(state.String(), func(t *testing.T) {
			lid := testutil.LeaseID(t)
			bus := pubsub.NewBus()
			defer bus.Close()
			sub, err := bus.Subscribe()
			require.NoError(t, err)
			defer sub.Close()
			client := clientmocks.NewClient(t)
			node := clientmocks.NewNodeClient(t)
			query := clientmocks.NewQueryClient(t)
			market := marketmocks.NewQueryClient(t)
			deployment := deploymentmocks.NewQueryClient(t)
			client.On("Node").Return(node)
			node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{}, nil)
			query.On("Market").Return(market)
			market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
				&mvbeta.QueryLeaseResponse{Lease: mtypes.Lease{ID: lid, State: state, Reason: mtypes.LeaseClosedReasonOwner}}, nil).Once()
			// A closed deployment's balance is irrelevant to local lease cleanup.
			query.On("Deployment").Return(deployment).Maybe()
			deployment.On("Deployment", mock.Anything, mock.Anything).Return(nil, errors.New("closed account")).Maybe()
			bc, err := newBalanceChecker(context.Background(), query, nil,
				session.New(log.NewNopLogger(), client, nil, 0), bus,
				BalanceCheckerConfig{LeaseFundsCheckInterval: time.Hour})
			require.NoError(t, err)
			defer bc.Close()
			require.NoError(t, bus.Publish(event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}))
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			for {
				select {
				case ev := <-sub.Events():
					if closed, ok := ev.(*mtypes.EventLeaseClosed); ok {
						require.Equal(t, lid, closed.ID)
						require.Equal(t, mtypes.LeaseClosedReasonOwner, closed.Reason)
						client.AssertNotCalled(t, "Tx")
						return
					}
				case <-deadline.C:
					t.Fatal("closed on-chain lease did not request local cleanup")
				}
			}
		})
	}
}

func (s *balanceTestSubscriber) Events() <-chan pubsub.Event { return s.events }
func (s *balanceTestSubscriber) Close()                      {}

type balanceTestBus struct {
	pubsub.Bus
	subscriber *balanceTestSubscriber
}

func (b *balanceTestBus) Subscribe() (pubsub.Subscriber, error) { return b.subscriber, nil }

func TestBalanceCheckerExpiredTimer(t *testing.T) {
	for _, remove := range []bool{true, false} {
		name := "shutdown"
		if remove {
			name = "remove monitor"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lid := testutil.LeaseID(t)
			fired := make(chan struct{})
			timer := time.AfterFunc(0, func() { close(fired) })
			<-fired
			subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
			bc := &balanceChecker{
				ctx: ctx, lc: lifecycle.New(), log: log.NewNopLogger(),
				bus:    &balanceTestBus{subscriber: subscriber},
				leases: map[mtypes.LeaseID]*leaseState{lid: {tm: timer}},
			}
			started, finished := make(chan error, 1), make(chan struct{})
			go func() {
				bc.run(started)
				close(finished)
			}()
			require.NoError(t, <-started)
			if remove {
				subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: lid}
				// Processing another event proves monitor removal did not hang.
				select {
				case subscriber.events <- struct{}{}:
				case <-time.After(time.Second):
					t.Fatal("removing a monitor with an expired timer blocked the balance checker")
				}
			}
			bc.lc.ShutdownAsync(nil)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("balance checker shutdown blocked on an expired timer")
			}
		})
	}
}
