package provider

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/boz/go-lifecycle"
	"pkg.akt.dev/go/sdkutil"

	"cosmossdk.io/log"
	tmrpc "github.com/cometbft/cometbft/rpc/core/types"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cltypes "pkg.akt.dev/go/node/client/types"
	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	emodule "pkg.akt.dev/go/node/escrow/module"
	etypes "pkg.akt.dev/go/node/escrow/types/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	"pkg.akt.dev/go/util/pubsub"
	netutil "pkg.akt.dev/node/v2/util/network"
	"pkg.akt.dev/node/v2/x/escrow/client/util"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

type respState int

const (
	respStateNextCheck = iota
	respStateOutOfFunds
	respStateScheduledWithdraw
)

// maxWithdrawBatch is the most leases one withdrawal transaction withdraws from.
const maxWithdrawBatch = 20

// isEscrowClosedErr reports whether a lease withdrawal failed with the escrow
// error code for a closed payment or account. The tx client maps a failed tx
// result's ABCI codespace and code back to the registered error. The chain
// never reopens either (an overdrawn payment fails with ErrPaymentClosed too),
// so the withdrawal can never succeed.
//
// Gas simulation failures carry no escrow code: the chain returns them as gRPC
// code Unknown (ABCI sdk/6 "unknown request"). They are retried, and the
// retry's funds check reads the payment and account states instead.
func isEscrowClosedErr(err error) bool {
	return errorsmod.IsOf(err, emodule.ErrPaymentClosed, emodule.ErrAccountClosed)
}

type BalanceCheckerConfig struct {
	// WithdrawalPeriod is how often the provider withdraws its earnings from each
	// lease. Zero turns withdrawals off: the checker then only checks balances.
	WithdrawalPeriod        time.Duration
	LeaseFundsCheckInterval time.Duration
}

type leaseState struct {
	tm                  *time.Timer
	scheduledWithdrawAt time.Time
	// closed is set once the checker has reported the lease closed. Until the
	// cluster removes it with LeaseRemoveFundsMonitor, the lease is not checked
	// or withdrawn from again, and late results for it are ignored.
	closed bool
	// withdrawing is set from when a withdrawal from the lease is queued until
	// its result arrives. The funds checks go on, but no other withdrawal from
	// the lease is queued meanwhile.
	withdrawing bool
	// retryWithdraw is set when a withdrawal failed, for a reason other than a
	// closed escrow. The lease's next funds check withdraws again.
	retryWithdraw bool
}

type balanceChecker struct {
	ctx     context.Context
	session session.Session
	log     log.Logger
	lc      lifecycle.Lifecycle
	bus     pubsub.Bus
	ownAddr sdk.AccAddress
	aqc     aclient.QueryClient
	leases  map[mtypes.LeaseID]*leaseState
	cfg     BalanceCheckerConfig
}

type leaseCheckResponse struct {
	lid        mtypes.LeaseID
	checkAfter time.Duration
	state      respState
	err        error
	closed     *mtypes.EventLeaseClosed
}

func newBalanceChecker(
	ctx context.Context,
	aqc aclient.QueryClient,
	accAddr sdk.AccAddress,
	clientSession session.Session,
	bus pubsub.Bus,
	cfg BalanceCheckerConfig,
) (*balanceChecker, error) {
	bc := &balanceChecker{
		ctx:     ctx,
		session: clientSession,
		log:     clientSession.Log().With("module", "balance-checker"),
		bus:     bus,
		lc:      lifecycle.New(),
		ownAddr: accAddr,
		aqc:     aqc,
		leases:  make(map[mtypes.LeaseID]*leaseState),
		cfg:     cfg,
	}

	if cfg.WithdrawalPeriod <= 0 {
		// No withdrawal settles the escrow of a lease out of funds, so the chain
		// does not close the lease.
		bc.log.Info("lease withdrawals are off: leases out of funds stay open until their escrow is settled")
	}

	startCh := make(chan error, 1)
	go bc.lc.WatchContext(ctx)
	go bc.run(startCh)

	select {
	case err := <-startCh:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return bc, nil
}

func (bc *balanceChecker) Close() error {
	bc.lc.Shutdown(nil)
	return bc.lc.Error()
}

func (bc *balanceChecker) runEscrowCheck(ctx context.Context, lid mtypes.LeaseID, scheduledWithdraw bool, res chan<- leaseCheckResponse) {
	go func() {
		select {
		case <-bc.lc.Done():
		case res <- bc.doEscrowCheck(ctx, lid, scheduledWithdraw):
		}
	}()
}

func (bc *balanceChecker) doEscrowCheck(ctx context.Context, lid mtypes.LeaseID, scheduledWithdraw bool) leaseCheckResponse {
	resp := leaseCheckResponse{
		lid:   lid,
		state: respStateNextCheck,
	}

	if scheduledWithdraw {
		resp.state = respStateScheduledWithdraw
	}

	var syncInfo *tmrpc.SyncInfo
	syncInfo, resp.err = bc.session.Client().Node().SyncInfo(ctx)
	if resp.err != nil {
		return resp
	}

	if syncInfo.CatchingUp {
		resp.err = aclient.ErrNodeNotSynced
		return resp
	}

	// Reconcile this lease even if its closure event was missed or its
	// persisted deployment was recovered after the lease closed.
	lease, err := bc.aqc.Market().Lease(ctx, &mvbeta.QueryLeaseRequest{ID: lid})
	if err != nil {
		resp.err = err
		return resp
	}
	if lease.Lease.ID != lid {
		resp.err = fmt.Errorf("lease query returned a different ID: %s", lease.Lease.ID)
		return resp
	}
	switch lease.Lease.State {
	case mtypes.LeaseClosed, mtypes.LeaseInsufficientFunds:
		resp.closed = &mtypes.EventLeaseClosed{ID: lid, Reason: lease.Lease.Reason}
		return resp
	case mtypes.LeaseActive, mtypes.LeaseReclaiming:
		// Both states still consume escrow funds. A low-funds withdrawal
		// lets the chain settle the account and decide whether to close it.
	default:
		resp.err = fmt.Errorf("unexpected lease state: %s", lease.Lease.State)
		return resp
	}

	// The chain can leave a lease active after its escrow payment closed or
	// was overdrawn. Neither state reopens, so withdrawal can never succeed:
	// close the lease locally as the chain's payment-closed hook would.
	// Reclaiming leases are included: that hook closes the lease whenever its
	// bid is active, so a live lease with a dead payment has an inactive bid,
	// and the reclamation close (MsgCloseBid) cannot succeed either.
	switch lease.EscrowPayment.State.State {
	case etypes.StateClosed:
		resp.closed = &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonUnspecified}
		return resp
	case etypes.StateOverdrawn:
		resp.closed = &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonInsufficientFunds}
		return resp
	}

	var dResp *dtypes.QueryDeploymentResponse

	// Fetch the balance of the escrow account
	dResp, resp.err = bc.aqc.Deployment().Deployment(ctx, &dtypes.QueryDeploymentRequest{
		ID: lid.DeploymentID(),
	})

	if resp.err != nil {
		return resp
	}

	// A closed or overdrawn escrow account never reopens either: a deposit to
	// an overdrawn account settles and closes it. Close the lease locally as
	// the chain's account-closed hook would.
	switch dResp.EscrowAccount.State.State {
	case etypes.StateClosed:
		resp.closed = &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonOwner}
		return resp
	case etypes.StateOverdrawn:
		resp.closed = &mtypes.EventLeaseClosed{ID: lid, Reason: mtypes.LeaseClosedReasonInsufficientFunds}
		return resp
	}

	totalLeaseAmount := sdkmath.LegacyNewDec(0)
	for _, state := range []mtypes.Lease_State{mtypes.LeaseActive, mtypes.LeaseReclaiming} {
		var lResp *mvbeta.QueryLeasesResponse
		lResp, resp.err = bc.aqc.Market().Leases(ctx, &mvbeta.QueryLeasesRequest{
			Filters: mtypes.LeaseFilters{
				Owner: lid.Owner,
				DSeq:  lid.DSeq,
				State: state.String(),
			},
		})
		if resp.err != nil {
			return resp
		}

		for _, lease := range lResp.Leases {
			totalLeaseAmount = totalLeaseAmount.Add(lease.Lease.Price.Amount)
		}
	}

	var balance sdkmath.LegacyDec
	for _, funds := range dResp.EscrowAccount.State.Funds {
		if funds.Denom == sdkutil.DenomUact {
			balance = funds.Amount
			break
		}
	}
	balanceRemain := util.LeaseCalcBalanceRemain(balance,
		syncInfo.LatestBlockHeight,
		dResp.EscrowAccount.State.SettledAt,
		totalLeaseAmount)

	blocksRemain := util.LeaseCalcBlocksRemain(balanceRemain, totalLeaseAmount)

	// lease is out of funds: check it again after the funds check interval
	if blocksRemain <= 0 {
		resp.state = respStateOutOfFunds
		resp.checkAfter = bc.cfg.LeaseFundsCheckInterval
	} else {
		blocksPerCheckInterval := int64(bc.cfg.LeaseFundsCheckInterval / netutil.AverageBlockTime)
		if blocksRemain > blocksPerCheckInterval {
			blocksRemain = blocksPerCheckInterval
		}

		resp.checkAfter = time.Duration(blocksRemain) * netutil.AverageBlockTime
	}

	return resp
}

// withdraw sends one transaction withdrawing from the lease of every item, and
// waits for its broadcast to finish, however long the tx queue holds it: the tx
// client still broadcasts a request whose caller gave up, so only its result
// tells the checker what happened. The transaction succeeds or fails as a
// whole. ctx ends only when the checker stops.
func (bc *balanceChecker) withdraw(ctx context.Context, items []withdrawItem) error {
	// The tx client can take a request even on an ended context.
	if err := ctx.Err(); err != nil {
		return err
	}

	msgs := make([]sdk.Msg, 0, len(items))
	for _, item := range items {
		msgs = append(msgs, &mvbeta.MsgWithdrawLease{ID: item.lid})
	}

	opts := []aclient.BroadcastOption{aclient.WithResultCodeAsError()}
	if len(msgs) > 1 {
		// The gas a batch needs grows with its leases, so it is estimated
		// whatever gas limit the provider is configured with.
		opts = append(opts, aclient.WithGas(cltypes.GasSetting{Simulate: true}))
	}

	_, err := bc.session.Client().Tx().BroadcastMsgs(ctx, msgs, opts...)
	return err
}

// withdrawItem is a lease withdrawal, and the state of the lease's monitor it
// was queued for. A lease removed and added again gets a new state, which the
// old monitor's withdrawal neither counts as nor touches.
type withdrawItem struct {
	lid   mtypes.LeaseID
	state *leaseState
}

// withdrawResult is the outcome of a withdrawal transaction.
type withdrawResult struct {
	items []withdrawItem
	err   error
}

// withdrawQueue holds the lease withdrawals waiting to be sent. The checker
// keeps at most one withdrawal transaction in flight, so it never fills the tx
// queue it shares with bids, and the withdrawals that fall due meanwhile go out
// together.
type withdrawQueue struct {
	// due holds the withdrawals that fell due, in that order.
	due []withdrawItem
	// alone holds the withdrawals of a failed batch. Each is sent in a
	// transaction of its own, so that each gets its own result.
	alone []withdrawItem
	// sending is set while a withdrawal transaction is in flight.
	sending bool
}

// next removes and returns the withdrawals of the next transaction, skipping
// those keep rejects: a withdrawal of a failed batch alone, otherwise up to
// maxWithdrawBatch due withdrawals.
func (q *withdrawQueue) next(keep func(withdrawItem) bool) []withdrawItem {
	for len(q.alone) > 0 {
		item := q.alone[0]
		q.alone = q.alone[1:]
		if keep(item) {
			return []withdrawItem{item}
		}
	}

	var batch []withdrawItem
	for len(q.due) > 0 && len(batch) < maxWithdrawBatch {
		item := q.due[0]
		q.due = q.due[1:]
		if keep(item) {
			batch = append(batch, item)
		}
	}

	return batch
}

// withdrawFailed handles a withdrawal from a single lease that failed. A closed
// escrow can never be withdrawn from, so the lease is torn down; any other
// failure is retried at the lease's next funds check.
func (bc *balanceChecker) withdrawFailed(item withdrawItem, err error) {
	lid, lState := item.lid, item.state
	lState.withdrawing = false

	// A lease removed, added again or reported closed while its withdrawal
	// was in flight is already being torn down, or monitored afresh.
	current := bc.leases[lid] == lState
	if !current || lState.closed || !isEscrowClosedErr(err) {
		bc.log.Error("failed to do lease withdrawal", "err", err, "lid", lid)
		if current && !lState.closed {
			lState.retryWithdraw = true
		}
		return
	}

	// The escrow closed after the funds check read it. Withdrawal cannot
	// succeed again, so tear the lease down instead of retrying it, with
	// the reason the funds check would report. ErrPaymentClosed cannot
	// tell a closed payment from an overdrawn one, so it is unspecified.
	reason := mtypes.LeaseClosedReasonUnspecified
	if errorsmod.IsOf(err, emodule.ErrAccountClosed) {
		reason = mtypes.LeaseClosedReasonOwner
	}
	bc.log.Info("lease escrow closed, tearing down deployment", "lid", lid, "err", err)
	// The cluster tears the lease down and removes its funds monitor with
	// LeaseRemoveFundsMonitor. If publishing fails, the already scheduled
	// funds check retries the teardown.
	if err := bc.bus.Publish(&mtypes.EventLeaseClosed{ID: lid, Reason: reason}); err != nil {
		bc.log.Error("unable to tear down lease with closed escrow", "err", err, "lid", lid)
		return
	}

	lState.closed = true
}

func (bc *balanceChecker) run(startCh chan<- error) {
	ctx, cancel := context.WithCancel(bc.ctx)

	defer func() {
		cancel()
		bc.lc.ShutdownCompleted()

		for _, lState := range bc.leases {
			if lState.tm != nil {
				lState.tm.Stop()
			}
		}

		bc.log.Debug("shutdown complete")
	}()

	leaseCheckCh := make(chan leaseCheckResponse, 1)
	withdrawResults := make(chan withdrawResult, 1)

	var withdrawals withdrawQueue
	sendWithdrawals := func() {
		// Withdrawals not yet sent are dropped once the checker stops.
		if withdrawals.sending || ctx.Err() != nil {
			return
		}

		batch := withdrawals.next(func(item withdrawItem) bool {
			// A lease removed or reported closed while its withdrawal waited is
			// being torn down. Added again, it has a new monitor.
			return bc.leases[item.lid] == item.state && !item.state.closed
		})
		if len(batch) == 0 {
			return
		}

		withdrawals.sending = true
		go func() {
			err := bc.withdraw(ctx, batch)
			select {
			case <-ctx.Done():
			case withdrawResults <- withdrawResult{items: batch, err: err}:
			}
		}()
	}

	subscriber, err := bc.bus.Subscribe()
	startCh <- err
	if err != nil {
		return
	}
	defer subscriber.Close()

loop:
	for {
		select {
		case shutdownErr := <-bc.lc.ShutdownRequest():
			bc.log.Debug("received shutdown request", "err", shutdownErr)
			bc.lc.ShutdownInitiated(shutdownErr)
			cancel()
			break loop
		case evt := <-subscriber.Events():
			switch ev := evt.(type) {
			case event.LeaseAddFundsMonitor:
				var scheduledWithdraw time.Time
				// if provider configured with periodic force withdrawal
				// set next time at which withdraw will happen
				if bc.cfg.WithdrawalPeriod > 0 {
					firstIn := bc.cfg.WithdrawalPeriod
					// The leases found at startup start monitoring together. Spread their
					// first withdrawals over the period, so they do not all queue at once.
					if !ev.IsNewLease {
						firstIn = time.Duration(rand.Int63n(int64(bc.cfg.WithdrawalPeriod))) // nolint: gosec
					}
					scheduledWithdraw = time.Now().Add(firstIn)
				}

				lState := &leaseState{
					scheduledWithdrawAt: scheduledWithdraw,
				}

				bc.leases[ev.LeaseID] = lState

				// if there was provider restart with a bunch of active leases
				// spread their requests across 1min interval
				// to reduce pressure on the RPC
				if !ev.IsNewLease {
					checkIn := time.Duration(rand.Int63n(int64(time.Minute))) // nolint: gosec
					lState.tm = bc.timerFunc(ctx, checkIn, ev.LeaseID, false, leaseCheckCh)
				} else {
					bc.runEscrowCheck(ctx, ev.LeaseID, false, leaseCheckCh)
				}
			case event.LeaseRemoveFundsMonitor:
				if lState, exists := bc.leases[ev.LeaseID]; exists {
					if lState.tm != nil {
						// AfterFunc timers have no channel to drain when already fired.
						lState.tm.Stop()
					}
					delete(bc.leases, ev.LeaseID)
				}
			}
		case res := <-leaseCheckCh:
			// we may have timer fired just a heart beat ahead of lease remove event.
			lState, exists := bc.leases[res.lid]
			if !exists || lState.closed {
				continue loop
			}

			if res.closed != nil {
				// The cluster tears the lease down and removes its funds monitor
				// with LeaseRemoveFundsMonitor. This does not send a transaction
				// or change the on-chain lease.
				res.err = bc.bus.Publish(res.closed)
				if res.err == nil {
					lState.closed = true
					continue loop
				}
			}
			if res.err != nil {
				bc.log.Info("couldn't check lease balance. retrying in 1m", "lid", res.lid, "err", res.err)
				lState.tm = bc.timerFunc(ctx, time.Minute, res.lid, res.state == respStateScheduledWithdraw, leaseCheckCh)
				continue loop
			}

			// A withdrawal that failed is sent again at the lease's next funds check.
			if lState.retryWithdraw && res.state == respStateNextCheck {
				res.state = respStateScheduledWithdraw
			}

			withdraw := false

			switch res.state {
			case respStateOutOfFunds:
				bc.log.Debug("lease is out of funds", "lid", res.lid)
				// A withdrawal settles the escrow, and the chain closes the lease if it
				// is not topped up. The funds check runs again either way.
				fallthrough
			case respStateScheduledWithdraw:
				// Without a withdrawal period the checker only checks balances: it
				// withdraws from no lease, not even one out of funds.
				if bc.cfg.WithdrawalPeriod > 0 {
					withdraw = true
					// reschedule periodic withdraw
					lState.scheduledWithdrawAt = time.Now().Add(bc.cfg.WithdrawalPeriod)
				}

				fallthrough
			case respStateNextCheck:
				timerPeriod := res.checkAfter
				scheduledWithdraw := false

				if !withdraw && !lState.scheduledWithdrawAt.IsZero() {
					withdrawIn := time.Until(lState.scheduledWithdrawAt)
					if timerPeriod >= withdrawIn {
						timerPeriod = withdrawIn
						scheduledWithdraw = true
					}
				}

				lState.tm = bc.timerFunc(ctx, timerPeriod, res.lid, scheduledWithdraw, leaseCheckCh)
			}

			switch {
			case withdraw && lState.withdrawing:
				// Its outstanding withdrawal settles the lease's earnings once broadcast.
				bc.log.Info("previous lease withdrawal still pending, not sending another", "lid", res.lid)
			case withdraw:
				bc.log.Debug("queueing lease withdrawal", "lid", res.lid)
				lState.withdrawing = true
				lState.retryWithdraw = false
				withdrawals.due = append(withdrawals.due, withdrawItem{lid: res.lid, state: lState})
				sendWithdrawals()
			}
		case res := <-withdrawResults:
			withdrawals.sending = false

			switch {
			case res.err == nil:
				for _, item := range res.items {
					item.state.withdrawing = false
				}
			case len(res.items) > 1:
				// One lease, with a closed escrow for instance, fails the whole
				// transaction. Each lease is withdrawn from again alone, so that each
				// gets its own result.
				bc.log.Info("lease withdrawal batch failed, withdrawing from each lease alone",
					"count", len(res.items), "err", res.err)
				withdrawals.alone = append(withdrawals.alone, res.items...)
			default:
				bc.withdrawFailed(res.items[0], res.err)
			}

			sendWithdrawals()
		}
	}
}

func (bc *balanceChecker) timerFunc(ctx context.Context, d time.Duration, lid mtypes.LeaseID, scheduledWithdraw bool, ch chan<- leaseCheckResponse) *time.Timer {
	return time.AfterFunc(d, func() {
		select {
		case <-ctx.Done():
		case ch <- bc.doEscrowCheck(ctx, lid, scheduledWithdraw):
		}
	})
}
