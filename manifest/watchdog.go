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
	leaseID          mtypes.LeaseID
	timeout          time.Duration
	broadcastTimeout time.Duration
	lc               lifecycle.Lifecycle
	sess             session.Session
	log              log.Logger
}

func newWatchdog(sess session.Session, parent <-chan struct{}, done chan<- dtypes.DeploymentID, leaseID mtypes.LeaseID, timeout, broadcastTimeout time.Duration) *watchdog {
	if broadcastTimeout <= 0 {
		broadcastTimeout = DefaultBroadcastTimeout
	}

	result := &watchdog{
		leaseID:          leaseID,
		timeout:          timeout,
		broadcastTimeout: broadcastTimeout,
		lc:               lifecycle.New(),
		sess:             sess,
		log:              sess.Log().With("leaseID", leaseID),
	}

	go result.lc.WatchChannel(parent)

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

// run waits for the manifest timeout, then broadcasts MsgCloseBid.
//
// Once the close-bid broadcast starts, it must not inherit watchdog shutdown
// cancellation. Otherwise a provider shutdown can leave the expired bid open.
func (wd *watchdog) run() {
	defer wd.lc.ShutdownCompleted()

	var runch <-chan runner.Result
	var err error

	wd.log.Debug("watchdog start")
	select {
	case <-time.After(wd.timeout):
		// Close the bid, since if this point is reached, then a manifest has not been received
		wd.log.Info("watchdog closing bid")

		runch = runner.Do(func() runner.Result {
			ctx, cancel := context.WithTimeout(context.Background(), wd.broadcastTimeout)
			defer cancel()

			msg := &mvbeta.MsgCloseBid{
				ID:     mtypes.MakeBidID(wd.leaseID.OrderID(), wd.sess.Provider().Address()),
				Reason: mtypes.LeaseClosedReasonManifestTimeout,
			}

			return runner.NewResult(wd.sess.Client().Tx().BroadcastMsgs(ctx, []sdk.Msg{msg}, aclient.WithResultCodeAsError()))
		})
	case err = <-wd.lc.ShutdownRequest():
	}

	shutdownRequest := wd.lc.ShutdownRequest()
	for runch != nil {
		select {
		case result := <-runch:
			if err := result.Error(); err != nil {
				wd.log.Error("failed closing bid", "err", err)
			}
			runch = nil
		case err = <-shutdownRequest:
			wd.log.Info("watchdog shutdown requested, waiting for bid close tx to complete")
			shutdownRequest = nil
		}
	}

	wd.lc.ShutdownInitiated(err)
}
