package provider

import (
	"context"
	"math/rand"
	"time"

	"github.com/boz/go-lifecycle"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/tendermint/tendermint/libs/log"
	tmrpc "github.com/tendermint/tendermint/rpc/core/types"

	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

	"github.com/akash-network/node/pubsub"
	netutil "github.com/akash-network/node/util/network"
	"github.com/akash-network/node/util/runner"
	"github.com/akash-network/node/x/escrow/client/util"

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

type BalanceCheckerConfig struct {
	WithdrawalPeriod        time.Duration
	LeaseFundsCheckInterval time.Duration
}

type leaseState struct {
	tm                  *time.Timer
	scheduledWithdrawAt time.Time
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
		log:     clientSession.Log().With("cmp", "balance-checker"),
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

	var dResp *dtypes.QueryDeploymentResponse
	var lResp *mtypes.QueryLeasesResponse

	// Fetch the balance of the escrow account
	dResp, resp.err = bc.aqc.Deployment(ctx, &dtypes.QueryDeploymentRequest{
		ID: lid.DeploymentID(),
	})

	if resp.err != nil {
		return resp
	}

	lResp, resp.err = bc.aqc.Leases(ctx, &mtypes.QueryLeasesRequest{
		Filters: mtypes.LeaseFilters{
			Owner: lid.Owner,
			DSeq:  lid.DSeq,
			State: mtypes.LeaseActive.String(),
		},
	})

	if resp.err != nil {
		return resp
	}

	totalLeaseAmount := sdk.NewDec(0)
	for _, lease := range lResp.Leases {
		totalLeaseAmount = totalLeaseAmount.Add(lease.Lease.Price.Amount)
	}

	balanceRemain := util.LeaseCalcBalanceRemain(dResp.EscrowAccount.TotalBalance().Amount,
		syncInfo.LatestBlockHeight,
		dResp.EscrowAccount.SettledAt,
		totalLeaseAmount)

	blocksRemain := util.LeaseCalcBlocksRemain(balanceRemain, totalLeaseAmount)

	// lease is out of funds
	if blocksRemain <= 0 {
		resp.state = respStateOutOfFunds
		resp.checkAfter = time.Minute * 10
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

	msg := &mtypes.MsgWithdrawLease{
		LeaseID: lid,
	}

	_, err := bc.session.Client().Tx().Broadcast(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError())
	return err
}

func (bc *balanceChecker) run(startCh chan<- error) {
	ctx, cancel := context.WithCancel(bc.ctx)

	defer func() {
		cancel()
		bc.lc.ShutdownCompleted()

		for _, lState := range bc.leases {
			if lState.tm != nil && !lState.tm.Stop() {
				<-lState.tm.C
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

	resultch = make(chan runner.Result, 1)

loop:
	for {
		select {
		case shutdownErr := <-bc.lc.ShutdownRequest():
			bc.log.Debug("received shutdown request", "err", shutdownErr)
			bc.lc.ShutdownInitiated(nil)
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
				lsState, exists := bc.leases[ev.LeaseID]
				if !exists {
					break
				}

				if lsState.tm != nil && !lsState.tm.Stop() {
					<-lsState.tm.C
				}

				delete(bc.leases, ev.LeaseID)
			}
		case res := <-leaseCheckCh:
			// we may have timer fired just a heart beat ahead of lease remove event.
			lState, exists := bc.leases[res.lid]
			if !exists {
				continue loop
			}

			withdraw := false

			switch res.state {
			case respStateOutOfFunds:
				bc.log.Debug("lease is out of funds", "lease", res.lid)
				// reschedule funds check. if lease not being topped up then network will close it
				fallthrough
			case respStateScheduledWithdraw:
				withdraw = true
				bc.log.Debug("sending withdraw", "lease", res.lid)
				// reschedule periodic withdraw if configured
				if bc.cfg.WithdrawalPeriod > 0 {
					lState.scheduledWithdrawAt = time.Now().Add(bc.cfg.WithdrawalPeriod)
				}

				fallthrough
			case respStateNextCheck:
				timerPeriod := res.checkAfter
				scheduledWithdraw := false

				if res.err != nil {
					bc.log.Info("couldn't check lease balance. retrying in 1m", "leaseId", res.lid, "error", res.err.Error())
					timerPeriod = time.Minute
				} else if !withdraw && !lState.scheduledWithdrawAt.IsZero() {
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
			if err := res.Error(); err != nil {
				bc.log.Error("failed to do lease withdrawal", "err", err, "LeaseID", res.Value().(mtypes.LeaseID))
			}
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
