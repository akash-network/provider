package provider

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/v1beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
)

// withdrawBatcher coalesces MsgWithdrawLease requests into single multi-msg
// transactions using opportunistic in-flight batching:
//
//   - Idle: Flush fires a 1-msg TX immediately.
//   - In-flight: subsequent Enqueue calls accumulate in pending.
//   - On MarkDone: callers invoke Flush which drains up to maxMsgs from pending.
//
// Not safe for concurrent use. All public methods must be called from a single
// goroutine. Concurrent calls panic to surface developer mistakes early.
type withdrawBatcher struct {
	tx      aclient.TxClient
	log     log.Logger
	timeout time.Duration
	maxMsgs int

	inUse atomic.Int32

	pending  []mtypes.LeaseID
	inFlight bool
	doneCh   chan error
}

func (b *withdrawBatcher) enter() {
	if !b.inUse.CompareAndSwap(0, 1) {
		panic("withdrawBatcher: concurrent use detected")
	}
}

func (b *withdrawBatcher) exit() {
	b.inUse.Store(0)
}

func newWithdrawBatcher(tx aclient.TxClient, logger log.Logger, timeout time.Duration, maxMsgs int) *withdrawBatcher {
	if maxMsgs < 1 {
		panic(fmt.Sprintf("withdrawBatcher: maxMsgs must be >= 1, got %d", maxMsgs))
	}
	return &withdrawBatcher{
		tx:      tx,
		log:     logger,
		timeout: timeout,
		maxMsgs: maxMsgs,
		doneCh:  make(chan error, 1),
	}
}

// After an in-flight broadcast fails, items coalesced during the in-flight
// window remain in pending (run-loop skips re-flush on error for natural
// backoff). If the same lease re-triggers before pending drains, Enqueue must
// dedupe so the next batch doesn't carry a duplicate MsgWithdrawLease, which
// would risk failing the entire atomic tx on the second message.
func (b *withdrawBatcher) Enqueue(lid mtypes.LeaseID) {
	b.enter()
	defer b.exit()
	if slices.Contains(b.pending, lid) {
		b.log.Debug("batcher: enqueue dedup", "lease", lid, "pending", len(b.pending), "inFlight", b.inFlight)
		return
	}
	b.pending = append(b.pending, lid)
	b.log.Debug("batcher: enqueue", "lease", lid, "pending", len(b.pending), "inFlight", b.inFlight)
}

// Remove drops a lease id from the pending batch.
// Does not affect an in-flight broadcast.
func (b *withdrawBatcher) Remove(lid mtypes.LeaseID) {
	b.enter()
	defer b.exit()
	b.pending = slices.DeleteFunc(b.pending, func(p mtypes.LeaseID) bool {
		return p == lid
	})
}

// InFlight reports whether a broadcast is currently running.
func (b *withdrawBatcher) InFlight() bool {
	b.enter()
	defer b.exit()
	return b.inFlight
}

// Pending reports the number of queued lease ids not yet broadcast.
func (b *withdrawBatcher) Pending() int {
	b.enter()
	defer b.exit()
	return len(b.pending)
}

// Flush starts a broadcast with up to maxMsgs pending lease ids when idle.
// Returns true if a broadcast was started, false if nothing to do or already in-flight.
func (b *withdrawBatcher) Flush(ctx context.Context) bool {
	b.enter()
	defer b.exit()
	if b.inFlight {
		b.log.Debug("batcher: flush skipped (in-flight)", "pending", len(b.pending))
		return false
	}
	if len(b.pending) == 0 {
		return false
	}

	n := min(len(b.pending), b.maxMsgs)

	batch := make([]mtypes.LeaseID, n)
	copy(batch, b.pending[:n])
	b.pending = b.pending[n:]
	b.inFlight = true

	b.log.Info("batcher: flush", "batch", n, "remaining", len(b.pending), "maxMsgs", b.maxMsgs)

	go func() {
		start := time.Now()
		err := b.broadcast(ctx, batch)
		b.log.Info("batcher: broadcast done", "batch", n, "duration", time.Since(start), "err", err)
		select {
		case <-ctx.Done():
		case b.doneCh <- err:
		}
	}()

	return true
}

// Done returns a channel that delivers the broadcast result of each completed batch.
// Callers must invoke MarkDone after reading to unblock the next Flush.
func (b *withdrawBatcher) Done() <-chan error {
	return b.doneCh
}

// MarkDone clears the in-flight flag. Must be called after reading Done().
func (b *withdrawBatcher) MarkDone() {
	b.enter()
	defer b.exit()
	b.inFlight = false
}

func (b *withdrawBatcher) broadcast(ctx context.Context, lids []mtypes.LeaseID) error {
	if len(lids) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	msgs := make([]sdk.Msg, 0, len(lids))
	for _, lid := range lids {
		msgs = append(msgs, &mvbeta.MsgWithdrawLease{ID: lid})
	}

	_, err := b.tx.BroadcastMsgs(ctx, msgs, aclient.WithResultCodeAsError())
	return err
}
