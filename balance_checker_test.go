package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	aclient "pkg.akt.dev/go/node/client/v1beta3"
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
	// A lease the chain reports closed is torn down whatever the withdrawal
	// period, since the teardown is not a withdrawal.
	for _, period := range []time.Duration{testWithdrawalPeriod, 0} {
		for _, state := range []mtypes.Lease_State{mtypes.LeaseClosed, mtypes.LeaseInsufficientFunds} {
			t.Run(fmt.Sprintf("period %v/%s", period, state), func(t *testing.T) {
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
					BalanceCheckerConfig{WithdrawalPeriod: period, LeaseFundsCheckInterval: time.Hour})
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

// testWithdrawalPeriod turns withdrawals on: without a withdrawal period the
// checker withdraws from no lease. A lease's first periodic withdrawal is due a
// period after it starts, long after any test ends.
const testWithdrawalPeriod = 24 * time.Hour

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
// without such a code keep the lease monitored, and are retried at its next
// funds check.
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
		{name: "tx client broadcast retry timed out", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lid := testutil.LeaseID(t)
			m := exhaustedLeaseClients(t, lid, tc.err)
			logs := logtest.New()
			subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
			bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
			// The out-of-funds recheck never fires during the test.
			bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
				BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: time.Hour})
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
			lState, monitored := bc.leases[lid]
			require.True(t, monitored, "only LeaseRemoveFundsMonitor removes a lease")
			require.Empty(t, bus.published, "unexpected extra publication")
			require.False(t, lState.withdrawing, "the withdrawal is still pending after its result")
			require.Equal(t, !tc.wantTeardown, lState.retryWithdraw,
				"only a lease that is not torn down is withdrawn from again at its next funds check")
		})
	}
}

// A withdrawal waits for its broadcast to finish, however long the tx queue
// holds it, so the checker acts on the outcome: a withdrawal that finds the
// escrow closed tears the lease down.
func TestBalanceCheckerWaitsForWithdrawalBroadcast(t *testing.T) {
	lid := testutil.LeaseID(t)
	m := exhaustedLeaseClients(t, lid, nil)
	// The queue holds the broadcast past any deadline its caller sets, and the
	// tx client then returns the caller's context error. Without a deadline the
	// broadcast finishes, and the chain reports the escrow closed.
	m.withdraw.Return(func(ctx context.Context, _ []sdk.Msg, _ ...aclient.BroadcastOption) (interface{}, error) {
		if _, ok := ctx.Deadline(); ok {
			return nil, context.DeadlineExceeded
		}
		return nil, cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")
	}, nil)
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logtest.New(), m.client, nil, 0), bus,
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: time.Hour})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}

	select {
	case ev := <-bus.published:
		require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
	case <-time.After(time.Second):
		t.Fatal("the checker gave up on the withdrawal before its broadcast finished")
	}
}

// A withdrawal waiting on its broadcast stops waiting when the checker stops.
func TestBalanceCheckerStopsWaitingForWithdrawalOnClose(t *testing.T) {
	lid := testutil.LeaseID(t)
	m := exhaustedLeaseClients(t, lid, nil)
	waiting, returned := make(chan struct{}), make(chan struct{})
	// The broadcast never finishes on its own.
	m.withdraw.Run(func(args mock.Arguments) {
		close(waiting)
		<-args.Get(0).(context.Context).Done()
		close(returned)
	})
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logtest.New(), m.client, nil, 0), bus,
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: time.Hour})
	require.NoError(t, err)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("exhausted lease did not send a withdrawal")
	}

	require.NoError(t, bc.Close())
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("the withdrawal kept waiting after the checker stopped")
	}
}

// While a withdrawal's broadcast is outstanding, the lease's funds checks go
// on, so a closed escrow is still noticed, but no other withdrawal is sent for
// the lease until that broadcast returns.
func TestBalanceCheckerSendsOneWithdrawalAtATime(t *testing.T) {
	lid := testutil.LeaseID(t)
	m := exhaustedLeaseClients(t, lid, nil)
	var broadcasts atomic.Int32
	held, release, resumed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.withdraw.Times(0).Run(func(args mock.Arguments) {
		switch broadcasts.Add(1) {
		case 1:
			close(held)
			select {
			case <-release:
			case <-args.Get(0).(context.Context).Done():
			}
		case 2:
			close(resumed)
		}
	})
	// Every recheck finds the lease still out of funds.
	rechecks := make(chan struct{}, 1)
	lease := mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
	m.node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 210}, nil).Maybe()
	m.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: leasePayment(lid, etypes.StateOpen)}, nil).
		Run(func(mock.Arguments) {
			select {
			case rechecks <- struct{}{}:
			default:
			}
		}).Maybe()
	m.deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
		&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
			State:     etypes.StateOpen,
			SettledAt: 100,
			Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1)}},
		}}}, nil).Maybe()
	m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, nil).Maybe()
	m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{}, nil).Maybe()
	logs := logtest.New()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0), bus,
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}

	await := func(ch <-chan struct{}, msg string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal(msg)
		}
	}
	await(held, "exhausted lease did not send a withdrawal")
	// Two funds checks run while the broadcast is outstanding.
	await(rechecks, "lease was not rechecked")
	await(rechecks, "lease was not rechecked twice")
	require.EqualValues(t, 1, broadcasts.Load(), "a second withdrawal was sent while one was outstanding")
	// A withdrawal that does not return keeps being reported, at the funds check interval.
	require.Eventually(t, func() bool {
		for _, entry := range logs.Entries() {
			if entry.Level == "info" && entry.Msg == "previous lease withdrawal still pending, not sending another" {
				return true
			}
		}
		return false
	}, time.Second, 5*time.Millisecond, "the outstanding withdrawal was not reported")

	// Once the outstanding broadcast returns, the next check withdraws again.
	close(release)
	await(resumed, "no withdrawal was sent after the outstanding one returned")
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
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: time.Hour})
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
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: 10 * time.Millisecond})
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
			BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: 10 * time.Millisecond})
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

// Once the checker stops, a withdrawal not yet sent is dropped: the tx client
// would take it even on an ended context.
func TestBalanceCheckerSendsNoWithdrawalAfterStopping(t *testing.T) {
	// The client has no tx client: a broadcast fails the test.
	bc := &balanceChecker{session: session.New(logtest.New(), clientmocks.NewClient(t), nil, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, bc.withdraw(ctx, []withdrawItem{{lid: testutil.LeaseID(t), state: &leaseState{}}}), context.Canceled)
}

// sharedLeaseClients holds chain client mocks shared by several leases, so
// their withdrawals go through one tx client.
type sharedLeaseClients struct {
	client     *clientmocks.Client
	tx         *clientmocks.TxClient
	query      *clientmocks.QueryClient
	market     *marketmocks.QueryClient
	deployment *deploymentmocks.QueryClient
}

func newSharedLeaseClients(t *testing.T) sharedLeaseClients {
	t.Helper()
	c := sharedLeaseClients{
		client:     clientmocks.NewClient(t),
		tx:         clientmocks.NewTxClient(t),
		query:      clientmocks.NewQueryClient(t),
		market:     marketmocks.NewQueryClient(t),
		deployment: deploymentmocks.NewQueryClient(t),
	}
	node := clientmocks.NewNodeClient(t)
	c.client.On("Node").Return(node).Maybe()
	c.client.On("Tx").Return(c.tx).Maybe()
	c.query.On("Market").Return(c.market).Maybe()
	c.query.On("Deployment").Return(c.deployment).Maybe()
	node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 200}, nil).Maybe()

	return c
}

// expectChecks registers funds checks of the lease that find its escrow
// payment in the given state, and an open payment's escrow balance exhausted,
// so such a check withdraws. times sets how many checks each query serves.
func (c sharedLeaseClients) expectChecks(lid mtypes.LeaseID, payment etypes.State, times func(*mock.Call) *mock.Call) {
	lease := mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
	times(c.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: leasePayment(lid, payment)}, nil))
	if payment != etypes.StateOpen {
		return
	}
	times(c.deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
		&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
			State:     etypes.StateOpen,
			SettledAt: 100,
			Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1)}},
		}}}, nil))
	times(c.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, nil))
	times(c.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{}, nil))
}

// expectExhausted registers one funds check of the lease that finds its escrow
// balance exhausted.
func (c sharedLeaseClients) expectExhausted(lid mtypes.LeaseID) {
	c.expectChecks(lid, etypes.StateOpen, (*mock.Call).Once)
}

// expectExhaustedRepeatedly registers any number of such funds checks.
func (c sharedLeaseClients) expectExhaustedRepeatedly(lid mtypes.LeaseID) {
	c.expectChecks(lid, etypes.StateOpen, (*mock.Call).Maybe)
}

// withdrawal is a withdrawal transaction sent through the shared tx client.
type withdrawal struct {
	leases []mtypes.LeaseID
	// options counts the broadcast options it was sent with.
	options int
}

// withdrawals records the withdrawal transactions sent through the shared tx
// client.
type withdrawals struct {
	mu   sync.Mutex
	sent []withdrawal
}

// recordWithdrawals answers every withdrawal transaction with answer, called
// with the leases it withdraws from.
func (c sharedLeaseClients) recordWithdrawals(answer func(context.Context, []mtypes.LeaseID) error) *withdrawals {
	w := &withdrawals{}
	broadcast := func(ctx context.Context, msgs []sdk.Msg, opts ...aclient.BroadcastOption) (interface{}, error) {
		lids := make([]mtypes.LeaseID, 0, len(msgs))
		for _, msg := range msgs {
			lids = append(lids, msg.(*mvbeta.MsgWithdrawLease).ID)
		}
		w.mu.Lock()
		w.sent = append(w.sent, withdrawal{leases: lids, options: len(opts)})
		w.mu.Unlock()

		return nil, answer(ctx, lids)
	}
	// The mock passes each broadcast option as an argument of its own: a single
	// withdrawal is sent with one, a batch with two.
	c.tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).Return(broadcast, nil).Maybe()
	c.tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(broadcast, nil).Maybe()

	return w
}

func (w *withdrawals) all() []withdrawal {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]withdrawal(nil), w.sent...)
}

// transactions returns the leases of each withdrawal transaction sent so far.
func (w *withdrawals) transactions() [][]mtypes.LeaseID {
	all := w.all()
	txs := make([][]mtypes.LeaseID, 0, len(all))
	for _, tx := range all {
		txs = append(txs, tx.leases)
	}

	return txs
}

// sends reports whether any withdrawal transaction sent so far withdraws from lid.
func (w *withdrawals) sends(lid mtypes.LeaseID) bool {
	for _, tx := range w.transactions() {
		for _, sent := range tx {
			if sent == lid {
				return true
			}
		}
	}

	return false
}

// startShared runs a checker on the shared clients that rechecks each lease at
// the given funds check interval, and returns its log.
func (c sharedLeaseClients) startShared(t *testing.T, bus *balanceTestBus, interval time.Duration) *logtest.Recorder {
	t.Helper()
	logs := logtest.New()
	bc, err := newBalanceChecker(context.Background(), c.query, nil, session.New(logs, c.client, nil, 0), bus,
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: interval})
	require.NoError(t, err)
	t.Cleanup(func() { _ = bc.Close() })

	return logs
}

const (
	// logQueued is what the checker logs as it queues a lease's withdrawal.
	logQueued = "queueing lease withdrawal"
	// logPending is what it logs when a funds check would withdraw from a lease
	// whose withdrawal is still pending.
	logPending = "previous lease withdrawal still pending, not sending another"
	// logWithdrawFailed is what it logs as it handles a failed withdrawal from
	// a single lease that it does not tear down.
	logWithdrawFailed = "failed to do lease withdrawal"
	// logOutOfFunds is what it logs as it handles a funds check that found the
	// lease out of funds.
	logOutOfFunds = "lease is out of funds"
	// logWithdrawalsOff is what it logs as it starts without a withdrawal period.
	logWithdrawalsOff = "lease withdrawals are off: leases out of funds stay open until their escrow is settled"
)

// Without a withdrawal period the checker says at startup that withdrawals are
// off: no withdrawal settles the escrow of a lease out of funds, so the chain
// does not close the lease.
func TestBalanceCheckerLogsWithdrawalsOffWithoutPeriod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		period time.Duration
		logged bool
	}{
		{name: "withdrawal period", period: testWithdrawalPeriod},
		{name: "no withdrawal period", logged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := logtest.New()
			bc, err := newBalanceChecker(context.Background(), clientmocks.NewQueryClient(t), nil,
				session.New(logs, clientmocks.NewClient(t), nil, 0),
				&balanceTestBus{subscriber: &balanceTestSubscriber{events: make(chan pubsub.Event)}},
				BalanceCheckerConfig{WithdrawalPeriod: tc.period, LeaseFundsCheckInterval: time.Hour})
			require.NoError(t, err)
			require.NoError(t, bc.Close())

			var levels []string
			for _, entry := range logs.Entries() {
				if entry.Msg == logWithdrawalsOff {
					levels = append(levels, entry.Level)
				}
			}
			if tc.logged {
				require.Equal(t, []string{"info"}, levels, "withdrawals off was not logged once at startup")
			} else {
				require.Empty(t, levels, "withdrawals off was logged with a withdrawal period")
			}
		})
	}
}

func countLogged(logs *logtest.Recorder, msg string) int {
	count := 0
	for _, entry := range logs.Entries() {
		if entry.Msg == msg {
			count++
		}
	}

	return count
}

// awaitLogged waits until msg is logged count times.
func awaitLogged(t *testing.T, logs *logtest.Recorder, msg string, count int) {
	t.Helper()
	require.Eventually(t, func() bool { return countLogged(logs, msg) >= count }, time.Second, time.Millisecond,
		"%q was not logged %d times", msg, count)
}

// holdFirst answers a withdrawal from first once release is closed, or the
// checker stops, and every other withdrawal with next.
func holdFirst(first mtypes.LeaseID, release <-chan struct{}, next func([]mtypes.LeaseID) error) func(context.Context, []mtypes.LeaseID) error {
	return func(ctx context.Context, lids []mtypes.LeaseID) error {
		if len(lids) == 1 && lids[0] == first {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}
		return next(lids)
	}
}

func succeed([]mtypes.LeaseID) error { return nil }

// With --withdrawal-period=0 the balance checker only checks balances. A lease
// out of funds is checked again at every funds check, but never withdrawn from.
func TestBalanceCheckerNeverWithdrawsWithoutWithdrawalPeriod(t *testing.T) {
	c := newSharedLeaseClients(t)
	lid := testutil.LeaseID(t)
	c.expectExhaustedRepeatedly(lid)
	w := c.recordWithdrawals(func(context.Context, []mtypes.LeaseID) error { return nil })
	logs := logtest.New()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bc, err := newBalanceChecker(context.Background(), c.query, nil, session.New(logs, c.client, nil, 0),
		&balanceTestBus{subscriber: subscriber}, BalanceCheckerConfig{LeaseFundsCheckInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	awaitLogged(t, logs, logOutOfFunds, 3)
	require.NoError(t, bc.Close())

	// Close waits for the event loop to exit, so its lease map is safe to read.
	lState := bc.leases[lid]
	require.Empty(t, w.transactions(), "a withdrawal was sent without a withdrawal period")
	require.False(t, lState.withdrawing, "a withdrawal was queued without a withdrawal period")
	// A withdrawal falling due would make the next funds check run at once.
	require.True(t, lState.scheduledWithdrawAt.IsZero(), "a withdrawal was scheduled without a withdrawal period")
}

// The first withdrawals of the leases found at startup are spread over the
// withdrawal period. Without a period none is scheduled for them either.
func TestBalanceCheckerSchedulesNoWithdrawalOfLeaseFoundAtStartupWithoutPeriod(t *testing.T) {
	client := clientmocks.NewClient(t)
	node := clientmocks.NewNodeClient(t)
	client.On("Node").Return(node).Maybe()
	// A funds check that falls due during the test fails, and is retried a minute later.
	node.On("SyncInfo", mock.Anything).Return(nil, errors.New("node unavailable")).Maybe()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bc, err := newBalanceChecker(context.Background(), clientmocks.NewQueryClient(t), nil,
		session.New(logtest.New(), client, nil, 0), &balanceTestBus{subscriber: subscriber},
		BalanceCheckerConfig{LeaseFundsCheckInterval: 10 * time.Minute})
	require.NoError(t, err)

	lid := testutil.LeaseID(t)
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid}
	require.NoError(t, bc.Close())

	// Close waits for the event loop to exit, so its lease map is safe to read.
	require.True(t, bc.leases[lid].scheduledWithdrawAt.IsZero(), "a withdrawal was scheduled without a withdrawal period")
}

// Leases that fall due while a withdrawal is pending wait for it, and are then
// withdrawn from together, in transactions of at most maxWithdrawBatch leases.
func TestBalanceCheckerBatchesWithdrawalsDueWhileOneIsPending(t *testing.T) {
	c := newSharedLeaseClients(t)
	first := testutil.LeaseID(t)
	c.expectExhausted(first)
	due := make([]mtypes.LeaseID, maxWithdrawBatch+1)
	for i := range due {
		due[i] = testutil.LeaseID(t)
		c.expectExhausted(due[i])
	}
	release := make(chan struct{})
	w := c.recordWithdrawals(holdFirst(first, release, succeed))
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	logs := c.startShared(t, &balanceTestBus{subscriber: subscriber}, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	for _, lid := range due {
		subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	}
	awaitLogged(t, logs, logQueued, len(due)+1)
	require.Never(t, func() bool { return len(w.transactions()) > 1 }, 100*time.Millisecond, 5*time.Millisecond,
		"a withdrawal was sent while another was pending")

	close(release)
	require.Eventually(t, func() bool { return len(w.transactions()) == 3 }, time.Second, time.Millisecond,
		"the due withdrawals were not sent")
	sent := w.all()
	require.Len(t, sent[1].leases, maxWithdrawBatch)
	require.Len(t, sent[2].leases, 1)
	require.ElementsMatch(t, due, append(sent[1].leases, sent[2].leases...))
	// The gas a batch needs grows with its leases, so the tx client estimates it
	// whatever gas limit the provider is configured with: a batch is sent with
	// one more broadcast option than a single withdrawal.
	require.Equal(t, sent[2].options+1, sent[1].options, "a batch does not have its gas estimated")
}

// A transaction succeeds or fails as a whole, so the leases of a failed batch
// are each withdrawn from again alone. Each lease then gets its own result: a
// closed escrow tears down only its own lease.
func TestBalanceCheckerSendsEachLeaseOfFailedBatchAlone(t *testing.T) {
	c := newSharedLeaseClients(t)
	first, open, closed := testutil.LeaseID(t), testutil.LeaseID(t), testutil.LeaseID(t)
	for _, lid := range []mtypes.LeaseID{first, open, closed} {
		c.expectExhausted(lid)
	}
	paymentClosed := cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")
	release := make(chan struct{})
	w := c.recordWithdrawals(holdFirst(first, release, func(lids []mtypes.LeaseID) error {
		for _, lid := range lids {
			if lid == closed {
				return paymentClosed
			}
		}
		return nil
	}))
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	logs := c.startShared(t, bus, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: open, IsNewLease: true}
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: closed, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 3)

	close(release)
	select {
	case ev := <-bus.published:
		require.Equal(t, &mtypes.EventLeaseClosed{ID: closed, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
	case <-time.After(time.Second):
		t.Fatal("the lease with a closed escrow was not torn down")
	}
	require.Eventually(t, func() bool { return len(w.transactions()) == 4 }, time.Second, time.Millisecond,
		"the leases of the failed batch were not withdrawn from alone")
	sent := w.transactions()
	require.ElementsMatch(t, []mtypes.LeaseID{open, closed}, sent[1])
	require.Equal(t, [][]mtypes.LeaseID{{sent[1][0]}, {sent[1][1]}}, sent[2:])
	require.Never(t, func() bool { return len(bus.published) > 0 }, 50*time.Millisecond, 5*time.Millisecond,
		"a lease with an open escrow was torn down")
}

// A lease removed while its withdrawal waits is being torn down, so its
// withdrawal is not sent.
func TestBalanceCheckerDropsQueuedWithdrawalOfRemovedLease(t *testing.T) {
	c := newSharedLeaseClients(t)
	first, removed := testutil.LeaseID(t), testutil.LeaseID(t)
	c.expectExhausted(first)
	c.expectExhausted(removed)
	release := make(chan struct{})
	w := c.recordWithdrawals(holdFirst(first, release, succeed))
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	logs := c.startShared(t, &balanceTestBus{subscriber: subscriber}, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: removed, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 2)

	subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: removed}
	close(release)
	require.Never(t, func() bool { return w.sends(removed) }, 100*time.Millisecond, 5*time.Millisecond,
		"a withdrawal was sent for a lease removed while it waited")
}

// A lease reported closed while its withdrawal waits is being torn down, so its
// withdrawal is not sent.
func TestBalanceCheckerDropsQueuedWithdrawalOfClosedLease(t *testing.T) {
	c := newSharedLeaseClients(t)
	first, closed := testutil.LeaseID(t), testutil.LeaseID(t)
	c.expectExhausted(first)
	c.expectExhaustedRepeatedly(first)
	c.expectExhausted(closed)
	// Its next funds check finds its escrow payment closed.
	c.expectChecks(closed, etypes.StateClosed, (*mock.Call).Once)
	release := make(chan struct{})
	w := c.recordWithdrawals(holdFirst(first, release, succeed))
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	logs := c.startShared(t, bus, 10*time.Millisecond)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: closed, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 2)
	select {
	case ev := <-bus.published:
		require.Equal(t, &mtypes.EventLeaseClosed{ID: closed, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
	case <-time.After(time.Second):
		t.Fatal("the lease was not reported closed")
	}

	close(release)
	require.Never(t, func() bool { return w.sends(closed) }, 100*time.Millisecond, 5*time.Millisecond,
		"a withdrawal was sent for a lease reported closed while it waited")
}

// A lease removed and added again while its withdrawal waits is monitored
// afresh: the withdrawal queued for its removed monitor is dropped, so the lease
// is withdrawn from once, for its new monitor.
func TestBalanceCheckerDropsQueuedWithdrawalOfReplacedMonitor(t *testing.T) {
	c := newSharedLeaseClients(t)
	first, readded := testutil.LeaseID(t), testutil.LeaseID(t)
	c.expectExhausted(first)
	// Each of the lease's two monitors checks it once.
	c.expectExhausted(readded)
	c.expectExhausted(readded)
	release := make(chan struct{})
	w := c.recordWithdrawals(holdFirst(first, release, succeed))
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	logs := c.startShared(t, &balanceTestBus{subscriber: subscriber}, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: readded, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 2)
	subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: readded}
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: readded, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 3)

	close(release)
	require.Eventually(t, func() bool { return len(w.transactions()) == 2 }, time.Second, time.Millisecond,
		"the new monitor's withdrawal was not sent")
	require.Never(t, func() bool { return len(w.transactions()) > 2 }, 100*time.Millisecond, 5*time.Millisecond,
		"the removed monitor's withdrawal was sent too")
	require.Equal(t, []mtypes.LeaseID{readded}, w.transactions()[1])
}

// The result of a removed monitor's withdrawal, success or failure, does not
// touch the lease's new monitor: while the new monitor's own withdrawal is
// pending, its funds checks queue no other.
func TestBalanceCheckerKeepsReplacedMonitorWithdrawalApart(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "removed monitor's withdrawal succeeds"},
		{name: "removed monitor's withdrawal fails", err: status.Error(codes.Unavailable, "connection refused")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newSharedLeaseClients(t)
			lid := testutil.LeaseID(t)
			c.expectExhausted(lid)
			c.expectExhaustedRepeatedly(lid)
			var broadcasts atomic.Int32
			started := make(chan struct{}, 2)
			release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
			c.recordWithdrawals(func(ctx context.Context, _ []mtypes.LeaseID) error {
				n := broadcasts.Add(1)
				if n <= 2 {
					started <- struct{}{}
					select {
					case <-release[n-1]:
					case <-ctx.Done():
					}
				}
				if n == 1 {
					return tc.err
				}
				return nil
			})
			subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
			logs := c.startShared(t, &balanceTestBus{subscriber: subscriber}, 10*time.Millisecond)
			awaitStarted := func(msg string) {
				t.Helper()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal(msg)
				}
			}

			subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
			awaitStarted("the first monitor's withdrawal was not sent")
			subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: lid}
			subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
			// The new monitor's withdrawal waits for the removed monitor's.
			awaitLogged(t, logs, logQueued, 2)
			close(release[0])
			awaitStarted("the new monitor's withdrawal was not sent")

			// Its funds checks go on while its withdrawal is pending.
			awaitLogged(t, logs, logPending, countLogged(logs, logPending)+2)
			require.Equal(t, 2, countLogged(logs, logQueued),
				"a second withdrawal was queued while the new monitor's was pending")
		})
	}
}

// The leases of a failed batch are each withdrawn from again alone. A lease
// removed and added again while it waits for that is monitored afresh: the
// withdrawal of its removed monitor is dropped, so the lease is withdrawn from
// once, for its new monitor.
func TestBalanceCheckerDropsFailedBatchWithdrawalOfReplacedMonitor(t *testing.T) {
	c := newSharedLeaseClients(t)
	first, alone, readded := testutil.LeaseID(t), testutil.LeaseID(t), testutil.LeaseID(t)
	c.expectExhausted(first)
	c.expectExhausted(alone)
	// Each of the lease's two monitors checks it once.
	c.expectExhausted(readded)
	c.expectExhausted(readded)
	releaseFirst, releaseAlone := make(chan struct{}), make(chan struct{})
	w := c.recordWithdrawals(func(ctx context.Context, lids []mtypes.LeaseID) error {
		var release <-chan struct{}
		switch {
		case len(lids) > 1:
			return status.Error(codes.Unavailable, "connection refused")
		case lids[0] == first:
			release = releaseFirst
		case lids[0] == alone:
			release = releaseAlone
		default:
			return nil
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	logs := c.startShared(t, &balanceTestBus{subscriber: subscriber}, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: first, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	// Queued one after the other, so that the failed batch's first lease is
	// withdrawn from alone first.
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: alone, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 2)
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: readded, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 3)
	close(releaseFirst)
	require.Eventually(t, func() bool { return len(w.transactions()) == 3 }, time.Second, time.Millisecond,
		"the leases of the failed batch were not withdrawn from alone")

	subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: readded}
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: readded, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 4)
	close(releaseAlone)
	require.Eventually(t, func() bool { return len(w.transactions()) == 4 }, time.Second, time.Millisecond,
		"the new monitor's withdrawal was not sent")
	require.Never(t, func() bool { return len(w.transactions()) > 4 }, 100*time.Millisecond, 5*time.Millisecond,
		"the removed monitor's withdrawal was sent too")
	require.Equal(t, [][]mtypes.LeaseID{{first}, {alone, readded}, {alone}, {readded}}, w.transactions())
}

// The result of a removed monitor's withdrawal is ignored, as for a removed
// lease, even when it finds the escrow closed: the lease's new monitor
// withdraws for itself, and tears the lease down once, from its own result.
func TestBalanceCheckerIgnoresClosedEscrowForReplacedMonitor(t *testing.T) {
	c := newSharedLeaseClients(t)
	lid := testutil.LeaseID(t)
	// Each of the lease's two monitors checks it once.
	c.expectExhausted(lid)
	c.expectExhausted(lid)
	paymentClosed := cerrors.ABCIError("escrow", 7, "failed to execute message; message index: 0")
	release := make(chan struct{})
	w := c.recordWithdrawals(func(ctx context.Context, _ []mtypes.LeaseID) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return paymentClosed
	})
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bus := &balanceTestBus{subscriber: subscriber, published: make(chan pubsub.Event, 8)}
	logs := c.startShared(t, bus, time.Hour)

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	require.Eventually(t, func() bool { return len(w.transactions()) == 1 }, time.Second, time.Millisecond,
		"exhausted lease did not send a withdrawal")
	subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: lid}
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	awaitLogged(t, logs, logQueued, 2)

	close(release)
	select {
	case ev := <-bus.published:
		require.Equal(t, &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonUnspecified}, ev)
	case <-time.After(time.Second):
		t.Fatal("the lease with a closed escrow was not torn down")
	}
	require.Never(t, func() bool { return len(bus.published) > 0 }, 100*time.Millisecond, 5*time.Millisecond,
		"the lease was torn down twice")
	require.Len(t, w.transactions(), 2, "the new monitor did not withdraw for itself")
}

// A withdrawal that fails for any reason but a closed escrow is sent again at
// the lease's next funds check, instead of waiting for the next withdrawal
// period or for the lease to run out of funds again.
func TestBalanceCheckerRetriesFailedWithdrawalAtNextCheck(t *testing.T) {
	lid := testutil.LeaseID(t)
	m := exhaustedLeaseClients(t, lid, nil)
	var broadcasts atomic.Int32
	retried := make(chan struct{})
	m.withdraw.Times(0).Return(func(context.Context, []sdk.Msg, ...aclient.BroadcastOption) (interface{}, error) {
		switch broadcasts.Add(1) {
		case 1:
			return nil, status.Error(codes.Unavailable, "connection refused")
		case 2:
			close(retried)
		}
		return nil, nil
	}, nil)
	// By its next funds check the lease has funds again. That check waits until
	// the failure has been handled, so it cannot overtake it.
	handled := make(chan struct{})
	lease := mtypes.Lease{ID: lid, State: mtypes.LeaseActive, Price: sdk.NewDecCoin("uact", sdkmath.NewInt(1))}
	m.node.On("SyncInfo", mock.Anything).Return(&tmrpc.SyncInfo{LatestBlockHeight: 210}, nil).Once().
		Run(func(args mock.Arguments) {
			select {
			case <-handled:
			case <-args.Get(0).(context.Context).Done():
			}
		})
	m.market.On("Lease", mock.Anything, &mvbeta.QueryLeaseRequest{ID: lid}).Return(
		&mvbeta.QueryLeaseResponse{Lease: lease, EscrowPayment: leasePayment(lid, etypes.StateOpen)}, nil).Once()
	m.deployment.On("Deployment", mock.Anything, &dtypes.QueryDeploymentRequest{ID: lid.DeploymentID()}).Return(
		&dtypes.QueryDeploymentResponse{EscrowAccount: etypes.Account{State: etypes.AccountState{
			State:     etypes.StateOpen,
			SettledAt: 100,
			Funds:     []etypes.Balance{{Denom: "uact", Amount: sdkmath.LegacyNewDec(1_000_000_000)}},
		}}}, nil).Once()
	m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseActive.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: lease}}}, nil).Once()
	m.market.On("Leases", mock.Anything, &mvbeta.QueryLeasesRequest{Filters: mtypes.LeaseFilters{
		Owner: lid.Owner, DSeq: lid.DSeq, State: mtypes.LeaseReclaiming.String(),
	}}).Return(&mvbeta.QueryLeasesResponse{}, nil).Once()
	// Later funds checks fail, and are retried a minute later.
	m.node.On("SyncInfo", mock.Anything).Return(nil, errors.New("node unavailable")).Maybe()
	logs := logtest.New()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bc, err := newBalanceChecker(context.Background(), m.query, nil, session.New(logs, m.client, nil, 0),
		&balanceTestBus{subscriber: subscriber},
		BalanceCheckerConfig{WithdrawalPeriod: testWithdrawalPeriod, LeaseFundsCheckInterval: 10 * time.Millisecond})
	require.NoError(t, err)
	defer bc.Close()

	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: lid, IsNewLease: true}
	// The event loop logs the failure while it handles it, and receives the next
	// check's result only once it is done.
	awaitLogged(t, logs, logWithdrawFailed, 1)
	close(handled)

	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("the failed withdrawal was not retried at the next funds check")
	}
}

// The leases found at startup start monitoring together, so their first
// periodic withdrawals are spread over the withdrawal period instead of all
// falling due at once. A new lease's first one is due a period after it starts.
func TestBalanceCheckerSpreadsWithdrawalsOfLeasesFoundAtStartup(t *testing.T) {
	const period = time.Hour
	client := clientmocks.NewClient(t)
	node := clientmocks.NewNodeClient(t)
	client.On("Node").Return(node).Maybe()
	// Funds checks that fall due during the test fail, and are retried a minute later.
	node.On("SyncInfo", mock.Anything).Return(nil, errors.New("node unavailable")).Maybe()
	subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
	bc, err := newBalanceChecker(context.Background(), clientmocks.NewQueryClient(t), nil,
		session.New(logtest.New(), client, nil, 0), &balanceTestBus{subscriber: subscriber},
		BalanceCheckerConfig{WithdrawalPeriod: period, LeaseFundsCheckInterval: 10 * time.Minute})
	require.NoError(t, err)

	start := time.Now()
	found := make([]mtypes.LeaseID, 50)
	for i := range found {
		found[i] = testutil.LeaseID(t)
		subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: found[i]}
	}
	created := testutil.LeaseID(t)
	subscriber.events <- event.LeaseAddFundsMonitor{LeaseID: created, IsNewLease: true}
	require.NoError(t, bc.Close())
	end := time.Now()

	// Close waits for the event loop to exit, so its lease map is safe to read.
	first, last := end.Add(period), start
	for _, lid := range found {
		at := bc.leases[lid].scheduledWithdrawAt
		require.False(t, at.Before(start), "first withdrawal due before monitoring started")
		require.False(t, at.After(end.Add(period)), "first withdrawal due more than a period away")
		if at.Before(first) {
			first = at
		}
		if at.After(last) {
			last = at
		}
	}
	require.Greater(t, last.Sub(first), period/2, "first withdrawals of the leases found at startup are not spread")

	at := bc.leases[created].scheduledWithdrawAt
	require.False(t, at.Before(start.Add(period)), "a new lease's first withdrawal is due before a period has passed")
	require.False(t, at.After(end.Add(period)), "a new lease's first withdrawal is due more than a period away")
}
