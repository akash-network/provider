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

// stop signals the watchdog to exit without closing the bid.
// Called when: (1) manifest received within the timeout, (2) manifest manager is stopped.
func (wd *watchdog) stop() {
	wd.lc.ShutdownAsync(nil)
}

// run waits for the manifest timeout, then broadcasts MsgCloseBid.
//
// Rule: once the broadcast is started, we commit to it - it must complete regardless of
// any concurrent stop() or parent shutdown. This prevents leaving an open bid on-chain.
// broadcastTimeout bounds how long we wait for the RPC response to prevent a permanent hang.
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
		// Manifest received or parent shutdown before timeout - exit without closing the bid.
	}

	// ShutdownRequest may arrive while we wait for the broadcast result.
	// Consume it to unblock the sender, but keep looping until runch delivers.
	// The broadcast context is independent and bounded by broadcastTimeout.
	for runch != nil {
		select {
		case result := <-runch:
			if err := result.Error(); err != nil {
				wd.log.Error("failed closing bid", "err", err)
			}
			runch = nil
		case err = <-wd.lc.ShutdownRequest():
			wd.log.Info("watchdog shutdown requested, waiting for bid close tx to complete")
		}
	}
	wd.lc.ShutdownInitiated(err)
}
