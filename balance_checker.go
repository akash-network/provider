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

	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	emodule "pkg.akt.dev/go/node/escrow/module"
	etypes "pkg.akt.dev/go/node/escrow/types/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"

	"pkg.akt.dev/go/util/pubsub"
	netutil "pkg.akt.dev/node/v2/util/network"
	"pkg.akt.dev/node/v2/util/runner"
	"pkg.akt.dev/node/v2/x/escrow/client/util"

	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
)

type respState int

const (
	withdrawTimeout = 30 * time.Second
)

const (
	respStateNextCheck = iota
	respStateOutOfFunds
	respStateScheduledWithdraw
)

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

func (bc *balanceChecker) startWithdraw(ctx context.Context, lid mtypes.LeaseID) error {
	ctx, cancel := context.WithTimeout(ctx, withdrawTimeout)
	defer cancel()

	msg := &mvbeta.MsgWithdrawLease{
		ID: lid,
	}

	_, err := bc.session.Client().Tx().BroadcastMsgs(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
	return err
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
	var resultch chan runner.Result

	subscriber, err := bc.bus.Subscribe()
	startCh <- err
	if err != nil {
		return
	}
	defer subscriber.Close()

	resultch = make(chan runner.Result, 1)

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
					scheduledWithdraw = time.Now().Add(bc.cfg.WithdrawalPeriod)
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

			withdraw := false

			switch res.state {
			case respStateOutOfFunds:
				bc.log.Debug("lease is out of funds", "lid", res.lid)
				// reschedule funds check. if lease not being topped up then network will close it
				fallthrough
			case respStateScheduledWithdraw:
				withdraw = true
				bc.log.Debug("sending withdraw", "lid", res.lid)
				// reschedule periodic withdraw if configured
				if bc.cfg.WithdrawalPeriod > 0 {
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

			if withdraw {
				go func() {
					select {
					case <-ctx.Done():
					case resultch <- runner.NewResult(res.lid, bc.startWithdraw(ctx, res.lid)):
					}
				}()
			}
		case res := <-resultch:
			err := res.Error()
			if err == nil {
				continue loop
			}

			lid := res.Value().(mtypes.LeaseID)
			// A lease removed or reported closed while its withdrawal was in
			// flight is already being torn down.
			lState, exists := bc.leases[lid]
			if !exists || lState.closed || !isEscrowClosedErr(err) {
				bc.log.Error("failed to do lease withdrawal", "err", err, "lid", lid)
				continue loop
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
				continue loop
			}

			lState.closed = true
		}
	}
}

func (bc *balanceChecker) timerFunc(ctx context.Context, d time.Duration, lid mtypes.LeaseID, scheduledWithdraw bool, ch chan<- leaseCheckResponse) *time.Timer {
	return time.AfterFunc(d, func() {
		select {
		case <-bc.ctx.Done():
		case ch <- bc.doEscrowCheck(ctx, lid, scheduledWithdraw):
		}
	})
}
