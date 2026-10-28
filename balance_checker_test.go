package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cerrors "cosmossdk.io/errors"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	"github.com/boz/go-lifecycle"
	tmrpc "github.com/cometbft/cometbft/rpc/core/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
	"github.com/akash-network/provider/tools/logging"
	"github.com/akash-network/provider/tools/logging/logtest"
)

type balanceTestSubscriber struct {
	pubsub.Subscriber
	events chan pubsub.Event
}

func TestBalanceCheckerPreservesNonterminalAndUncertainLeases(t *testing.T) {
	for _, tc := range []struct {
		name           string
		state          mtypes.Lease_State
		queryErr       error
		catchingUp     bool
		wrongID        bool
		noPayment      bool
		noAccountState bool
		wantErr        bool
	}{
		{name: "active", state: mtypes.LeaseActive},
		{name: "reclaiming", state: mtypes.LeaseReclaiming},
		// A response without the escrow payment (zero value) is not a terminal payment.
		{name: "payment not reported", state: mtypes.LeaseActive, noPayment: true},
		// Likewise, an escrow account without a state is not a terminal account.
		{name: "account state not reported", state: mtypes.LeaseActive, noAccountState: true},
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
				payment := leasePayment(lid, etypes.StateOpen)
				if tc.noPayment {
					payment = etypes.Payment{}
				}
				market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
					&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: payment}, tc.queryErr).Once()
			}
			if tc.state == mtypes.LeaseActive || tc.state == mtypes.LeaseReclaiming {
				deployment := deploymentmocks.NewQueryClient(t)
				query.On("Deployment").Return(deployment)
				funded := fundedDeployment()
				if tc.noAccountState {
					funded.EscrowAccount.State.State = etypes.StateInvalid
				}
				deployment.On("Deployment", mock.Anything, mock.Anything).Return(funded, nil)
				for _, state := range []mtypes.Lease_State{mtypes.LeaseActive, mtypes.LeaseReclaiming} {
					leases := &mvbeta.QueryLeasesResponse{}
					if state == tc.state {
						leases.Leases = []mvbeta.QueryLeaseResponse{{Lease: mtypes.Lease{
							ID: lid, State: state, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1)),
						}}}
					}
					market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
						Owner: lid.Owner, DSeq: lid.DSeq, State: state.String(),
					}}).Return(leases, nil).Once()
				}
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

func TestBalanceCheckerReclaimingLeaseFunds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		balance           int64
		activeSibling     bool
		scheduledWithdraw bool
		queryErr          error
		wantState         respState
	}{
		{name: "funded", balance: 150, wantState: respStateNextCheck},
		{name: "exhausted without periodic withdrawals", balance: 1, wantState: respStateOutOfFunds},
		{name: "shared escrow exhausted", balance: 150, activeSibling: true, wantState: respStateOutOfFunds},
		{name: "scheduled withdrawal", balance: 150, scheduledWithdraw: true, wantState: respStateScheduledWithdraw},
		{name: "reclaiming query failed", queryErr: errors.New("RPC unavailable"), wantState: respStateNextCheck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			lease := mtypes.Lease{ID: lid, State: mtypes.LeaseReclaiming, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
			client := clientmocks.NewClient(t)
			node := clientmocks.NewNodeClient(t)
			query := clientmocks.NewQueryClient(t)
			market := marketmocks.NewQueryClient(t)
			deployment := deploymentmocks.NewQueryClient(t)
			client.On("Node").Return(node)
			node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 200}, nil)
			query.On("Market").Return(market)
			market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
				&mvbeta.QueryLeaseResponse{Lease: lease}, nil).Once()
			query.On("Deployment").Return(deployment)
			deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
				&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
					SettledAt: 100,
					Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(tc.balance)}},
				}}}, nil).Once()
			active := &mvbeta.QueryLeasesResponse{}
			if tc.activeSibling {
				sibling := lease
				sibling.ID.GSeq++
				sibling.ID.Provider = testutil.AccAddress(t).String()
				sibling.State = mtypes.LeaseActive
				active.Leases = []mvbeta.QueryLeaseResponse{{Lease: sibling}}
			}
			market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
				Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
			}}).Return(active, nil).Once()
			market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
				Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
			}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, tc.queryErr).Once()
			bc := &balanceChecker{
				aqc: query, session: session.New(log.NewNopLogger(), client, nil, 0),
				// An interval no other constant in the checker uses.
				cfg: BalanceCheckerConfig{LeaseFundsCheckInterval: 7 * time.Minute, WithdrawalPeriod: 0},
			}

			res := bc.doEscrowCheck(context.Background(), lid, tc.scheduledWithdraw)
			require.Nil(t, res.closed, "only confirmed terminal chain state may request teardown")
			require.Equal(t, tc.wantState, res.state)
			if tc.queryErr != nil {
				require.ErrorIs(t, res.err, tc.queryErr)
			} else {
				require.NoError(t, res.err)
				require.Positive(t, res.checkAfter)
			}
			if tc.wantState == respStateOutOfFunds {
				require.Equal(t, 7*time.Minute, res.checkAfter, "an out-of-funds lease is checked again after the funds check interval")
			}
		})
	}
}

func leasePayment(lid mtypes.LeaseID, state etypes.State) etypes.Payment {
	return etypes.Payment{ID: lid.ToEscrowPaymentID(), State: etypes.PaymentState{
		Owner: lid.Provider,
		State: state,
		Rate:  sdk.NewDecCoin("uact", sdkmath.NewInt(1)),
	}}
}

// The chain can leave a lease active after its escrow payment or account closed
// or was overdrawn, e.g. when the escrow hook skipped the lease. None of these
// states reopens, so the lease is closed with the reason the chain hook would
// use, without querying lease prices or withdrawing.
func TestBalanceCheckerClosesLeaseWithTerminalEscrow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lease      mtypes.Lease_State
		payment    etypes.State
		account    etypes.State // queried only when the payment is open
		wantReason mtypes.LeaseClosedReason
	}{
		{name: "active lease, closed payment", lease: mtypes.LeaseActive,
			payment: etypes.StateClosed, wantReason: mtypes.LeaseClosedReasonUnspecified},
		{name: "active lease, overdrawn payment", lease: mtypes.LeaseActive,
			payment: etypes.StateOverdrawn, wantReason: mtypes.LeaseClosedReasonInsufficientFunds},
		{name: "reclaiming lease, closed payment", lease: mtypes.LeaseReclaiming,
			payment: etypes.StateClosed, wantReason: mtypes.LeaseClosedReasonUnspecified},
		{name: "reclaiming lease, overdrawn payment", lease: mtypes.LeaseReclaiming,
			payment: etypes.StateOverdrawn, wantReason: mtypes.LeaseClosedReasonInsufficientFunds},
		{name: "active lease, closed account", lease: mtypes.LeaseActive, payment: etypes.StateOpen,
			account: etypes.StateClosed, wantReason: mtypes.LeaseClosedReasonOwner},
		{name: "active lease, overdrawn account", lease: mtypes.LeaseActive, payment: etypes.StateOpen,
			account: etypes.StateOverdrawn, wantReason: mtypes.LeaseClosedReasonInsufficientFunds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			client := clientmocks.NewClient(t)
			node := clientmocks.NewNodeClient(t)
			query := clientmocks.NewQueryClient(t)
			market := marketmocks.NewQueryClient(t)
			client.On("Node").Return(node)
			node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 200}, nil)
			query.On("Market").Return(market)
			market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(&mvbeta.QueryLeaseResponse{
				Lease:         mtypes.Lease{ID: lid, State: tc.lease, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))},
				EscrowPayment: leasePayment(lid, tc.payment),
			}, nil).Once()
			if tc.payment == etypes.StateOpen {
				// A closed or overdrawn account has refunded its deposits.
				deployment := deploymentmocks.NewQueryClient(t)
				query.On("Deployment").Return(deployment)
				deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
					&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{
						ID: lid.DeploymentID().ToEscrowAccountID(),
						State: etypes.AccountState{
							Owner:     lid.Owner,
							State:     tc.account,
							SettledAt: 150,
							Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyZeroDec()}},
						},
					}}, nil).Once()
			}
			bc := &balanceChecker{
				aqc: query, session: session.New(log.NewNopLogger(), client, nil, 0),
				cfg: BalanceCheckerConfig{LeaseFundsCheckInterval: time.Minute},
			}

			res := bc.doEscrowCheck(context.Background(), lid, true)
			require.NoError(t, res.err)
			require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: tc.wantReason}, res.closed)
			client.AssertNotCalled(t, "Tx")
		})
	}
}

func fundedDeployment() *dtypes.QueryDeploymentResponse {
	return &dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
		State: etypes.StateOpen,
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
						require.NoError(t, bc.Close())
						// Close waits for the event loop to exit, so its lease map is safe to read.
						_, monitored := bc.leases[lid]
						require.True(t, monitored, "the checker only issues EventLeaseClosed; "+
							"the cluster removes the funds monitor with LeaseRemoveFundsMonitor")
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
	published  chan pubsub.Event
}

func (b *balanceTestBus) Subscribe() (pubsub.Subscriber, error) { return b.subscriber, nil }

// Publish records synchronously, so an event published by the balance checker
// is visible before any later log entry. A bus built without a published
// channel expects no events and fails loudly instead of blocking.
func (b *balanceTestBus) Publish(ev pubsub.Event) error {
	if b.published == nil {
		panic(fmt.Sprintf("unexpected publish: %T", ev))
	}
	b.published <- ev
	return nil
}

// exhaustedLease holds chain client mocks for an active lease whose escrow
// balance is exhausted, so its first funds check sends a withdrawal that fails
// with the given error. Tests add expectations for later checks to node and
// market, and can hold the withdrawal through withdraw.
type exhaustedLease struct {
	client     *clientmocks.Client
	node       *clientmocks.NodeClient
	query      *clientmocks.QueryClient
	market     *marketmocks.QueryClient
	deployment *deploymentmocks.QueryClient
	withdraw   *mock.Call
}

func exhaustedLeaseClients(t *testing.T, lid mtypes.LeaseID, withdrawErr error) exhaustedLease {
	t.Helper()
	lease := mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
	client := clientmocks.NewClient(t)
	node := clientmocks.NewNodeClient(t)
	tx := clientmocks.NewTxClient(t)
	query := clientmocks.NewQueryClient(t)
	market := marketmocks.NewQueryClient(t)
	deployment := deploymentmocks.NewQueryClient(t)
	client.On("Node").Return(node)
	node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 200}, nil).Once()
	query.On("Market").Return(market)
	// The funds check sees an open payment; the withdrawal then finds the escrow closed.
	market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: leasePayment(lid, etypes.StateOpen)}, nil).Once()
	query.On("Deployment").Return(deployment)
	deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
		&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
			State:     etypes.StateOpen,
			SettledAt: 100,
			Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1)}},
		}}}, nil).Once()
	market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, nil).Once()
	market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{}, nil).Once()
	client.On("Tx").Return(tx)
	withdraw := tx.On("BroadcastMsgs", mock.Anything, []sdk.Msg{&mvbeta.MsgWithdrawLease{ID: lid}}, mock.Anything).
		Return(nil, withdrawErr).Once()

	return exhaustedLease{client: client, node: node, query: query, market: market, deployment: deployment, withdraw: withdraw}
}

// expectLeaseCheck registers the queries of one more funds check of the lease:
// the chain reports it active with the given payment state, and for an open
// payment an exhausted escrow account.
func (m exhaustedLease) expectLeaseCheck(lid mtypes.LeaseID, payment etypes.State) *mock.Call {
	lease := mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
	m.node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 210}, nil).Once()
	call := m.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: leasePayment(lid, payment)}, nil).Once()
	if payment == etypes.StateOpen {
		m.deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
			&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
				State:     etypes.StateOpen,
				SettledAt: 100,
				Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1)}},
			}}}, nil).Once()
		m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
			Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
		}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, nil).Once()
		m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
			Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
		}}).Return(&mvbeta.QueryLeasesResponse{}, nil).Once()
	}

	return call
}

// holdUntil makes a mocked call wait until release is closed, or until the
// caller's context ends when the checker shuts down. started is closed once
// the call is waiting.
func holdUntil(call *mock.Call, started, release chan struct{}) {
	call.Run(func(args mock.Arguments) {
		close(started)
		select {
		case <-release:
		case <-args.Get(0).(context.Context).Done():
		}
	})
}

func loggedError(logs *logtest.Recorder, err error) bool {
	for _, entry := range logs.Entries() {
		if logged, ok := entry.KV[logging.KeyErr].(error); ok && entry.Level == "error" && logged == err {
			return true
		}
	}

	return false
}

// A withdrawal that fails with the escrow code for a closed payment or account
// can never succeed, so the lease is torn down instead of retried. Failures
// without such a code keep the lease monitored and are retried as before.
func TestBalanceCheckerWithdrawFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		wantTeardown bool
		wantReason   mtypes.LeaseClosedReason
	}{
		// A failed tx result carries the escrow codespace and code. escrow/7
		// cannot tell a closed payment from an overdrawn one; escrow/2 is a
		// closed account, whose leases the chain closes with reason Owner.
		{name: "payment closed tx result", wantTeardown: true, wantReason: mtypes.LeaseClosedReasonUnspecified,
			err: cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")},
		{name: "account closed tx result", wantTeardown: true, wantReason: mtypes.LeaseClosedReasonOwner,
			err: cerrors.ABCIError("escrow", 2, "failed to execute message; message index: 0")},
		// Gas simulation flattens the chain error into text under gRPC code
		// Unknown (ABCI sdk/6 "unknown request"), so it carries no escrow code.
		// This is the error from providers stuck retrying; the retry tears the
		// lease down instead (TestBalanceCheckerRetryTearsDownAfterUncodedWithdrawFailure).
		{name: "payment closed in simulation", err: status.Error(codes.Unknown,
			"rpc error: code = Unknown desc = failed to execute message; message index: 0: payment closed "+
				"[cosmos/cosmos-sdk@v0.53.6/baseapp/baseapp.go:1052] with gas used: '42512': unknown request")},
		{name: "provider cannot pay fees",
			err: cerrors.ABCIError("sdk", 5, "spendable balance 10uakt is smaller than 5000uakt")},
		{name: "account sequence mismatch",
			err: cerrors.ABCIError("sdk", 32, "account sequence mismatch, expected 8, got 7")},
		{name: "payment not found",
			err: cerrors.ABCIError("escrow", 8, "failed to execute message; message index: 0")},
		{name: "node unavailable", err: status.Error(codes.Unavailable, "connection refused")},
		{name: "withdrawal timed out", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			m := exhaustedLeaseClients(t, lid, tc.err)
			logs := logtest.New()
			subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
			bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
			// The out-of-funds recheck never fires during the test.
			bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
				BalanceCheckerConfig{LeaseFundsCheckInterval: time.Hour})
			require.NoError(t, err)
			// Stops the checker if an assertion fails first; Close is idempotent.
			defer bc.Close()

			subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}

			if tc.wantTeardown {
				select {
				case ev := <-bus.published:
					require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: tc.wantReason}, ev)
				case <-time.After(time.Second):
					t.Fatal("withdrawal from a closed escrow did not tear down the lease")
				}
			} else {
				require.Eventually(t, func() bool { return loggedError(logs, tc.err) },
					time.Second, 5*time.Millisecond, "withdrawal failure was not reported")
				select {
				case ev := <-bus.published:
					t.Fatalf("retryable withdrawal failure published %T", ev)
				default:
				}
			}

			require.NoError(t, bc.Close())
			// Close waits for the event loop to exit, so its lease map is safe to read.
			// The checker never drops a lease itself, not even one it reported closed:
			// the cluster removes the funds monitor with LeaseRemoveFundsMonitor.
			_, monitored := bc.leases[lid]
			require.True(t, monitored, "only LeaseRemoveFundsMonitor removes a lease")
			require.Empty(t, bus.published, "unexpected extra publication")
		})
	}
}

// Removing a lease's funds monitor means its teardown is already under way, so
// a closed-escrow withdrawal failure that arrives afterwards must not tear the
// lease down a second time.
func TestBalanceCheckerIgnoresClosedEscrowForRemovedLease(t *testing.T) {
	lid := testutil.LeaseID(t)
	withdrawErr := cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")
	m := exhaustedLeaseClients(t, lid, withdrawErr)
	sent, release := make(chan struct{}), make(chan struct{})
	m.withdraw.Run(func(mock.Arguments) {
		close(sent)
		<-release
	})
	logs := logtest.New()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
		BalanceCheckerConfig{LeaseFundsCheckInterval: time.Hour})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("exhausted lease did not send a withdrawal")
	}
	// The unbuffered send completes once the event loop has taken the removal,
	// which it handles before it can receive the withdrawal result.
	subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: lid}
	close(release)

	require.Eventually(t, func() bool { return loggedError(logs, withdrawErr) },
		time.Second, 5*time.Millisecond, "withdrawal failure was not reported")
	select {
	case ev := <-bus.published:
		t.Fatalf("withdrawal failure for a removed lease published %T", ev)
	default:
	}
}

// Gas simulation strips the escrow code from a withdrawal failure, so the
// failure alone does not show that the escrow closed. The lease stays monitored,
// and its retry after the funds check interval reads the closed payment and
// tears the lease down without withdrawing again.
func TestBalanceCheckerRetryTearsDownAfterUncodedWithdrawFailure(t *testing.T) {
	lid := testutil.LeaseID(t)
	withdrawErr := status.Error(codes.Unknown,
		"rpc error: code = Unknown desc = failed to execute message; message index: 0: payment closed "+
			"[cosmos/cosmos-sdk@v0.53.6/baseapp/baseapp.go:1052] with gas used: '42512': unknown request")
	m := exhaustedLeaseClients(t, lid, withdrawErr)
	// The retry's funds check finds the payment closed. It is held until the
	// withdrawal failure is handled, so the failure always comes first.
	failureHandled := make(chan struct{})
	m.node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 210}, nil).Once()
	m.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(&mvbeta.QueryLeaseResponse{
		Lease:         mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))},
		EscrowPayment: leasePayment(lid, etypes.StateClosed),
	}, nil).Once().Run(func(args mock.Arguments) {
		select {
		case <-failureHandled:
		case <-args.Get(0).(context.Context).Done():
		}
	})
	logs := logtest.New()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
		BalanceCheckerConfig{LeaseFundsCheckInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	require.Eventually(t, func() bool { return loggedError(logs, withdrawErr) },
		time.Second, 5*time.Millisecond, "withdrawal failure was not reported")
	select {
	case ev := <-bus.published:
		t.Fatalf("uncoded withdrawal failure published %T", ev)
	default:
	}
	close(failureHandled)

	select {
	case ev := <-bus.published:
		require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
	case <-time.After(time.Second):
		t.Fatal("retry after an uncoded withdrawal failure did not tear down the lease")
	}
	// Until the cluster removes its funds monitor, a lease reported closed is not
	// checked again: another check would hit an unexpected chain query.
	require.Never(t, func() bool { return len(bus.published) > 0 },
		100*time.Millisecond, 10*time.Millisecond, "lease reported closed more than once")

	require.NoError(t, bc.Close())
	// Close waits for the event loop to exit, so its lease map is safe to read.
	_, monitored := bc.leases[lid]
	require.True(t, monitored, "only LeaseRemoveFundsMonitor removes a lease")
	require.Empty(t, bus.published, "lease torn down more than once")
}

// Once the checker has reported a lease closed, it no longer acts on the lease
// while the cluster removes it with LeaseRemoveFundsMonitor: not on its
// scheduled check, nor on a check or withdrawal that was already in flight.
func TestBalanceCheckerIgnoresLeaseReportedClosed(t *testing.T) {
	codedErr := cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")
	// start runs a checker whose rechecks fall due within the test.
	start := func(t *testing.T, m exhaustedLease, lid mtypes.LeaseID) (*balanceChecker, *balanceTestBus, *logtest.Recorder) {
		t.Helper()
		logs := logtest.New()
		subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
		bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
		bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
			BalanceCheckerConfig{LeaseFundsCheckInterval: 10 * time.Millisecond})
		require.NoError(t, err)
		t.Cleanup(func() { _ = bc.Close() })
		subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}

		return bc, bus, logs
	}
	awaitReport := func(t *testing.T, bus *balanceTestBus, lid mtypes.LeaseID) {
		t.Helper()
		select {
		case ev := <-bus.published:
			require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
		case <-time.After(time.Second):
			t.Fatal("lease was not reported closed")
		}
	}
	awaitHeld := func(t *testing.T, held chan struct{}) {
		t.Helper()
		select {
		case <-held:
		case <-time.After(time.Second):
			t.Fatal("chain call was not made")
		}
	}
	// reportedOnce checks that nothing else was published or sent, and that the
	// lease is left for the cluster to remove.
	reportedOnce := func(t *testing.T, bc *balanceChecker, bus *balanceTestBus, lid mtypes.LeaseID) {
		t.Helper()
		require.Never(t, func() bool { return len(bus.published) > 0 },
			100*time.Millisecond, 10*time.Millisecond, "lease reported closed more than once")
		require.NoError(t, bc.Close())
		// Close waits for the event loop to exit, so its lease map is safe to read.
		_, monitored := bc.leases[lid]
		require.True(t, monitored, "only LeaseRemoveFundsMonitor removes a lease")
		require.Empty(t, bus.published, "lease reported closed more than once")
	}

	t.Run("scheduled check after a coded withdrawal failure", func(t *testing.T) {
		lid := testutil.LeaseID(t)
		m := exhaustedLeaseClients(t, lid, codedErr)
		// The recheck armed when the withdrawal started would find the payment
		// closed and report the lease again.
		m.node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 210}, nil).Maybe()
		m.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(&mvbeta.QueryLeaseResponse{
			Lease:         mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))},
			EscrowPayment: leasePayment(lid, etypes.StateClosed),
		}, nil).Maybe()
		bc, bus, _ := start(t, m, lid)

		awaitReport(t, bus, lid)
		reportedOnce(t, bc, bus, lid)
	})

	t.Run("check in flight during a coded withdrawal failure", func(t *testing.T) {
		lid := testutil.LeaseID(t)
		m := exhaustedLeaseClients(t, lid, codedErr)
		withdrawing, releaseWithdraw := make(chan struct{}), make(chan struct{})
		holdUntil(m.withdraw, withdrawing, releaseWithdraw)
		// The recheck reads the escrow while it is still open; its out-of-funds
		// result would send a second withdrawal, which the strict mock rejects.
		checking, releaseCheck := make(chan struct{}), make(chan struct{})
		holdUntil(m.expectLeaseCheck(lid, etypes.StateOpen), checking, releaseCheck)
		bc, bus, _ := start(t, m, lid)

		awaitHeld(t, checking)
		close(releaseWithdraw)
		awaitReport(t, bus, lid)
		close(releaseCheck)
		reportedOnce(t, bc, bus, lid)
	})

	t.Run("coded withdrawal failure after a check reported the lease closed", func(t *testing.T) {
		lid := testutil.LeaseID(t)
		m := exhaustedLeaseClients(t, lid, codedErr)
		withdrawing, releaseWithdraw := make(chan struct{}), make(chan struct{})
		holdUntil(m.withdraw, withdrawing, releaseWithdraw)
		// The recheck runs while the withdrawal is held and finds the payment closed.
		m.expectLeaseCheck(lid, etypes.StateClosed)
		bc, bus, logs := start(t, m, lid)

		awaitHeld(t, withdrawing)
		awaitReport(t, bus, lid)
		close(releaseWithdraw)
		// The late failure is still reported, but not as a second closure.
		require.Eventually(t, func() bool { return loggedError(logs, codedErr) },
			time.Second, 5*time.Millisecond, "late withdrawal failure was not reported")
		reportedOnce(t, bc, bus, lid)
	})
}

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
			// The event loop has exited, so its lease map is safe to read.
			_, monitored := bc.leases[lid]
			require.Equal(t, !remove, monitored, "LeaseRemoveFundsMonitor removes the lease")
		})
	}
}
