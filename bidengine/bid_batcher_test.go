package bidengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
)

func testBidLogger() log.Logger { return log.NewLogger(io.Discard) }

func newBidReq() bidRequest {
	return bidRequest{
		msg:     &sdk.TxResponse{}, // placeholder non-nil msg
		replyCh: make(chan error, 1),
	}
}

func waitReply(t *testing.T, req bidRequest) error {
	t.Helper()
	select {
	case err := <-req.replyCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reply")
		return nil
	}
}

func waitDone(t *testing.T, b *bidBatcher) {
	t.Helper()
	select {
	case <-b.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batcher done")
	}
}

// bidFixture sets up a bidBatcher with a controllable mock TxClient.
type bidFixture struct {
	t        *testing.T
	tx       *clientmocks.TxClient
	batcher  *bidBatcher
	release  chan struct{}
	captured chan []sdk.Msg
}

func newBidFixture(t *testing.T, maxMsgs int, block bool) *bidFixture {
	t.Helper()
	f := &bidFixture{
		t:        t,
		tx:       &clientmocks.TxClient{},
		release:  make(chan struct{}),
		captured: make(chan []sdk.Msg, 16),
	}
	f.tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			f.captured <- args.Get(1).([]sdk.Msg)
			if block {
				<-f.release
			}
		}).
		Return(&sdk.TxResponse{}, nil)
	f.batcher = newBidBatcher(f.tx, testBidLogger(), time.Second, maxMsgs)
	return f
}

func (f *bidFixture) waitCaptured() []sdk.Msg {
	f.t.Helper()
	select {
	case msgs := <-f.captured:
		return msgs
	case <-time.After(2 * time.Second):
		f.t.Fatal("timed out waiting for broadcast")
		return nil
	}
}

// --- parseMsgFailIndex ---

func TestParseMsgFailIndex_valid(t *testing.T) {
	err := errors.New("tx failed: message index: 2: insufficient funds")
	require.Equal(t, 2, parseMsgFailIndex(err))
}

func TestParseMsgFailIndex_zero(t *testing.T) {
	err := errors.New("message index: 0: bad")
	require.Equal(t, 0, parseMsgFailIndex(err))
}

func TestParseMsgFailIndex_no_index(t *testing.T) {
	err := errors.New("out of gas")
	require.Equal(t, -1, parseMsgFailIndex(err))
}

func TestParseMsgFailIndex_whitespace(t *testing.T) {
	err := errors.New("message index:   7: bad msg")
	require.Equal(t, 7, parseMsgFailIndex(err))
}

// --- Flush / Done / MarkDone state machine ---

func TestBidBatcher_idle_flush_succeeds(t *testing.T) {
	f := newBidFixture(t, 10, false)
	req := newBidReq()

	f.batcher.Enqueue(req)
	require.True(t, f.batcher.Flush(context.Background()))

	msgs := f.waitCaptured()
	require.Len(t, msgs, 1)
	require.NoError(t, waitReply(t, req))
	waitDone(t, f.batcher)
}

func TestBidBatcher_flush_when_empty_is_noop(t *testing.T) {
	b := newBidBatcher(&clientmocks.TxClient{}, testBidLogger(), time.Second, 10)
	require.False(t, b.Flush(context.Background()))
}

func TestBidBatcher_flush_when_in_flight_is_noop(t *testing.T) {
	f := newBidFixture(t, 10, true)
	req1 := newBidReq()
	req2 := newBidReq()

	f.batcher.Enqueue(req1)
	require.True(t, f.batcher.Flush(context.Background()))
	f.waitCaptured()

	// Now in-flight: second Flush must return false.
	f.batcher.Enqueue(req2)
	require.False(t, f.batcher.Flush(context.Background()))

	// Unblock and drain.
	close(f.release)
	waitDone(t, f.batcher)
	require.NoError(t, waitReply(t, req1))
}

func TestBidBatcher_respects_max_msgs(t *testing.T) {
	f := newBidFixture(t, 2, false)
	reqs := make([]bidRequest, 5)
	for i := range reqs {
		reqs[i] = newBidReq()
		f.batcher.Enqueue(reqs[i])
	}

	require.True(t, f.batcher.Flush(context.Background()))
	msgs := f.waitCaptured()
	require.Len(t, msgs, 2)
	require.Equal(t, 3, f.batcher.Pending())

	waitDone(t, f.batcher)
	f.batcher.MarkDone()

	// Second flush sends next 2.
	require.True(t, f.batcher.Flush(context.Background()))
	msgs = f.waitCaptured()
	require.Len(t, msgs, 2)
}

func TestBidBatcher_all_replies_nil_on_success(t *testing.T) {
	f := newBidFixture(t, 10, false)
	reqs := make([]bidRequest, 3)
	for i := range reqs {
		reqs[i] = newBidReq()
		f.batcher.Enqueue(reqs[i])
	}
	f.batcher.Flush(context.Background())
	f.waitCaptured()
	waitDone(t, f.batcher)

	for _, req := range reqs {
		require.NoError(t, waitReply(t, req))
	}
}

// --- Error handling ---

func TestBidBatcher_unrecoverable_error_fails_all(t *testing.T) {
	tx := &clientmocks.TxClient{}
	broadcastErr := errors.New("out of gas")
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, broadcastErr)

	b := newBidBatcher(tx, testBidLogger(), time.Second, 10)
	reqs := []bidRequest{newBidReq(), newBidReq(), newBidReq()}
	for _, r := range reqs {
		b.Enqueue(r)
	}
	b.Flush(context.Background())
	waitDone(t, b)

	for _, req := range reqs {
		err := waitReply(t, req)
		require.ErrorIs(t, err, broadcastErr)
	}
}

func TestBidBatcher_smart_retry_removes_failing_msg(t *testing.T) {
	tx := &clientmocks.TxClient{}
	badErr := fmt.Errorf("tx failed: message index: 1: bad msg")
	// First broadcast of 3 msgs fails at index 1.
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, badErr).Once()
	// Retry of remaining 2 msgs (index 0 and 2 from original) succeeds.
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(&sdk.TxResponse{}, nil).Once()

	b := newBidBatcher(tx, testBidLogger(), time.Second, 10)
	req0 := newBidReq()
	req1 := newBidReq()
	req2 := newBidReq()
	b.Enqueue(req0)
	b.Enqueue(req1)
	b.Enqueue(req2)
	b.Flush(context.Background())
	waitDone(t, b)

	require.NoError(t, waitReply(t, req0))
	require.ErrorIs(t, waitReply(t, req1), badErr)
	require.NoError(t, waitReply(t, req2))
}

func TestBidBatcher_smart_retry_cascade(t *testing.T) {
	tx := &clientmocks.TxClient{}
	err0 := fmt.Errorf("message index: 0: bad")
	err1 := fmt.Errorf("message index: 0: bad")
	// Broadcast 3 msgs: index 0 fails.
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, err0).Once()
	// Retry 2 remaining: index 0 (originally index 1) fails.
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, err1).Once()
	// Retry 1 remaining: succeeds.
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(&sdk.TxResponse{}, nil).Once()

	b := newBidBatcher(tx, testBidLogger(), time.Second, 10)
	reqs := []bidRequest{newBidReq(), newBidReq(), newBidReq()}
	for _, r := range reqs {
		b.Enqueue(r)
	}
	b.Flush(context.Background())
	waitDone(t, b)

	require.ErrorIs(t, waitReply(t, reqs[0]), err0)
	require.ErrorIs(t, waitReply(t, reqs[1]), err1)
	require.NoError(t, waitReply(t, reqs[2]))
}

func TestBidBatcher_context_cancel_stops_broadcast(t *testing.T) {
	tx := &clientmocks.TxClient{}
	started := make(chan struct{})
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { close(started) }).
		Return(nil, context.Canceled)

	ctx, cancel := context.WithCancel(context.Background())
	b := newBidBatcher(tx, testBidLogger(), time.Second, 10)
	req := newBidReq()
	b.Enqueue(req)
	b.Flush(ctx)

	<-started
	cancel()

	waitDone(t, b)
	require.ErrorIs(t, waitReply(t, req), context.Canceled)
}
