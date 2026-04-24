package bidengine

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/v1beta3"
)

// bidRequest is a single MsgCreateBid submission with a reply channel.
type bidRequest struct {
	msg     sdk.Msg
	replyCh chan error
}

// reMsgIndex matches the Cosmos SDK error format "message index: N" to identify
// which message in a multi-msg tx failed.
var reMsgIndex = regexp.MustCompile(`message index:\s*(\d+)`)

// parseMsgFailIndex extracts the 0-based failing message index from a Cosmos SDK
// tx error. Returns -1 if the error does not identify a specific message.
func parseMsgFailIndex(err error) int {
	m := reMsgIndex.FindStringSubmatch(err.Error())
	if m == nil {
		return -1
	}
	idx, e := strconv.Atoi(m[1])
	if e != nil {
		return -1
	}
	return idx
}

// bidBatcher coalesces MsgCreateBid requests into single multi-msg transactions
// using opportunistic in-flight batching.
//
// On broadcast failure, it parses the Cosmos SDK error to find which message
// failed, fans the error to that caller, removes it, and retries the remaining
// messages. This continues until all messages are resolved individually.
//
// Not safe for concurrent use. All methods must be called from service.run().
type bidBatcher struct {
	tx      aclient.TxClient
	log     log.Logger
	timeout time.Duration
	maxMsgs int

	pending  []bidRequest
	inFlight bool
	doneCh   chan struct{}
}

func newBidBatcher(tx aclient.TxClient, logger log.Logger, timeout time.Duration, maxMsgs int) *bidBatcher {
	if maxMsgs < 1 {
		panic(fmt.Sprintf("bidBatcher: maxMsgs must be >= 1, got %d", maxMsgs))
	}
	return &bidBatcher{
		tx:      tx,
		log:     logger,
		timeout: timeout,
		maxMsgs: maxMsgs,
		doneCh:  make(chan struct{}, 1),
	}
}

func (b *bidBatcher) InFlight() bool {
	return b.inFlight
}

func (b *bidBatcher) Pending() int {
	return len(b.pending)
}

func (b *bidBatcher) Enqueue(req bidRequest) {
	b.pending = append(b.pending, req)
	b.log.Debug("bid batcher: enqueue", "pending", len(b.pending), "inFlight", b.inFlight)
}

// Flush starts a broadcast with up to maxMsgs pending requests when idle.
// Returns true if a broadcast was started.
func (b *bidBatcher) Flush(ctx context.Context) bool {
	if b.inFlight {
		b.log.Debug("bid batcher: flush skipped (in-flight)", "pending", len(b.pending))
		return false
	}
	if len(b.pending) == 0 {
		return false
	}

	n := len(b.pending)
	if n > b.maxMsgs {
		n = b.maxMsgs
	}

	batch := make([]bidRequest, n)
	copy(batch, b.pending[:n])
	b.pending = b.pending[n:]
	b.inFlight = true

	b.log.Info("bid batcher: flush", "batch", n, "remaining", len(b.pending), "maxMsgs", b.maxMsgs)

	go func() {
		b.broadcastWithRetry(ctx, batch)
		select {
		case <-ctx.Done():
		case b.doneCh <- struct{}{}:
		}
	}()

	return true
}

// Done returns a channel that signals when the current batch is fully resolved.
// Call MarkDone after receiving, then Flush to start the next batch.
func (b *bidBatcher) Done() <-chan struct{} {
	return b.doneCh
}

// MarkDone clears the in-flight flag. Must be called after receiving from Done().
func (b *bidBatcher) MarkDone() {
	b.inFlight = false
}

// broadcastWithRetry broadcasts remaining requests, removing individual failures
// by parsing the Cosmos SDK "message index: N" error. Each resolved request
// receives its own error or nil via its replyCh.
func (b *bidBatcher) broadcastWithRetry(ctx context.Context, batch []bidRequest) {
	remaining := batch

	for len(remaining) > 0 {
		msgs := make([]sdk.Msg, len(remaining))
		for i, req := range remaining {
			msgs[i] = req.msg
		}

		broadcastCtx, cancel := context.WithTimeout(ctx, b.timeout)
		_, err := b.tx.BroadcastMsgs(broadcastCtx, msgs, aclient.WithResultCodeAsError(), aclient.WithPriority())
		cancel()

		if err == nil {
			b.log.Info("bid batcher: batch succeeded", "count", len(remaining))
			for _, req := range remaining {
				req.replyCh <- nil
			}
			return
		}

		idx := parseMsgFailIndex(err)
		if idx < 0 || idx >= len(remaining) {
			// Error is not message-specific (e.g. network/sequence error): fail all.
			b.log.Error("bid batcher: unrecoverable batch error", "err", err, "remaining", len(remaining))
			for _, req := range remaining {
				req.replyCh <- err
			}
			return
		}

		b.log.Error("bid batcher: message failed, retrying remainder", "idx", idx, "err", err, "remaining", len(remaining)-1)
		remaining[idx].replyCh <- err
		remaining = slices.Delete(remaining, idx, idx+1)
	}
}
