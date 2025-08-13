package manifest

import (
	"context"
	"time"

	"github.com/boz/go-lifecycle"

	"cosmossdk.io/log"

	sdk "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/util/runner"

	"github.com/akash-network/provider/session"
)

type watchdog struct {
	leaseID mtypes.LeaseID
	timeout time.Duration
	lc      lifecycle.Lifecycle
	sess    session.Session
	ctx     context.Context
	log     log.Logger
}

func newWatchdog(sess session.Session, parent <-chan struct{}, done chan<- dtypes.DeploymentID, leaseID mtypes.LeaseID, timeout time.Duration) *watchdog {
	ctx, cancel := context.WithCancel(context.Background())
	result := &watchdog{
		leaseID: leaseID,
		timeout: timeout,
		lc:      lifecycle.New(),
		sess:    sess,
		ctx:     ctx,
		log:     sess.Log().With("leaseID", leaseID),
	}

	go func() {
		result.lc.WatchChannel(parent)
		cancel()
	}()

	go func() {
		<-result.lc.Done()
		done <- leaseID.DeploymentID()
	}()

	go result.run()

	return result
}

func (wd *watchdog) stop() {
	wd.lc.ShutdownAsync(nil)
}

func (wd *watchdog) run() {
	defer wd.lc.ShutdownCompleted()

	var runch <-chan runner.Result
	var err error

	wd.log.Debug("watchdog start")
	select {
	case <-time.After(wd.timeout):
		// Close the bid, since if this point is reached then a manifest has not been received
		wd.log.Info("watchdog closing bid")

		runch = runner.Do(func() runner.Result {
			msg := &mvbeta.MsgCloseBid{
				ID: mtypes.MakeBidID(wd.leaseID.OrderID(), wd.sess.Provider().Address()),
			}

			return runner.NewResult(wd.sess.Client().Tx().BroadcastMsgs(wd.ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError()))
		})
	case err = <-wd.lc.ShutdownRequest():
	}

	wd.lc.ShutdownInitiated(err)
	if runch != nil {
		result := <-runch
		if err := result.Error(); err != nil {
			wd.log.Error("failed closing bid", "err", err)
		}
	}
}
