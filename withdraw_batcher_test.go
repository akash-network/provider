package provider

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/testutil"
)

func testLogger() log.Logger { return log.NewLogger(io.Discard) }

// batcherFixture wires a withdrawBatcher to a mock TxClient whose broadcast
// can be blocked until released, enabling deterministic in-flight tests.
type batcherFixture struct {
	t        *testing.T
	tx       *clientmocks.TxClient
	batcher  *withdrawBatcher
	captured chan []sdk.Msg
	release  chan struct{}
}

func newBatcherFixture(t *testing.T, maxMsgs int, block bool) *batcherFixture {
	t.Helper()

	f := &batcherFixture{
		t:        t,
		tx:       &clientmocks.TxClient{},
		captured: make(chan []sdk.Msg, 16),
		release:  make(chan struct{}),
	}

	f.tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			msgs := args.Get(1).([]sdk.Msg)
			f.captured <- msgs
			if block {
				<-f.release
			}
		}).
		Return(&sdk.TxResponse{}, nil)

	f.batcher = newWithdrawBatcher(f.tx, testLogger(), time.Second, maxMsgs)

	return f
}

func (f *batcherFixture) waitCaptured() []sdk.Msg {
	f.t.Helper()
	select {
	case msgs := <-f.captured:
		return msgs
	case <-time.After(2 * time.Second):
		f.t.Fatal("timed out waiting for broadcast")
		return nil
	}
}

func (f *batcherFixture) waitDone() error {
	f.t.Helper()
	select {
	case err := <-f.batcher.Done():
		return err
	case <-time.After(2 * time.Second):
		f.t.Fatal("timed out waiting for batch completion")
		return nil
	}
}

func lidsOf(t *testing.T, n int) []mtypes.LeaseID {
	t.Helper()
	out := make([]mtypes.LeaseID, n)
	for i := 0; i < n; i++ {
		out[i] = testutil.LeaseID(t)
	}
	return out
}

// happy path: enqueue one withdrawal and flush it immediately
func TestWithdrawBatcher_IdleFlushImmediate(t *testing.T) {
	f := newBatcherFixture(t, 50, false)
	lid := testutil.LeaseID(t)

	f.batcher.Enqueue(lid)
	require.True(t, f.batcher.Flush(context.Background()))

	msgs := f.waitCaptured()
	require.Len(t, msgs, 1)
	require.Equal(t, lid, msgs[0].(*mvbeta.MsgWithdrawLease).ID)

	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()
	require.False(t, f.batcher.Flush(context.Background()))
}

// send the first withdrawal tx to force the batcher start batching the remaining withdrawals
func TestWithdrawBatcher_BurstCoalescesWhileInFlight(t *testing.T) {
	f := newBatcherFixture(t, 50, true)
	lids := lidsOf(t, 5)

	f.batcher.Enqueue(lids[0])
	require.True(t, f.batcher.Flush(context.Background()))

	first := f.waitCaptured()
	require.Len(t, first, 1)

	for _, l := range lids[1:] {
		f.batcher.Enqueue(l)
	}
	require.False(t, f.batcher.Flush(context.Background()))
	require.Equal(t, 4, f.batcher.Pending())

	close(f.release)
	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()
	require.True(t, f.batcher.Flush(context.Background()))

	second := f.waitCaptured()
	require.Len(t, second, 4)
	for i, m := range second {
		require.Equal(t, lids[i+1], m.(*mvbeta.MsgWithdrawLease).ID)
	}
}

// set the limit to 2 and send 5 withdrawals, the batcher should batch the first 2, second 2, and the last 1
func TestWithdrawBatcher_RespectsMaxMsgs(t *testing.T) {
	f := newBatcherFixture(t, 2, false)
	lids := lidsOf(t, 5)

	for _, l := range lids {
		f.batcher.Enqueue(l)
	}

	require.True(t, f.batcher.Flush(context.Background()))
	first := f.waitCaptured()
	require.Len(t, first, 2)
	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()

	require.True(t, f.batcher.Flush(context.Background()))
	second := f.waitCaptured()
	require.Len(t, second, 2)
	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()

	require.True(t, f.batcher.Flush(context.Background()))
	third := f.waitCaptured()
	require.Len(t, third, 1)
	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()

	require.False(t, f.batcher.Flush(context.Background()))
}

// enqueue several withdrawals and remove one.
// on flush, the batcher should batch the remaining withdrawals and the removed one should be ignored
func TestWithdrawBatcher_RemoveFiltersPending(t *testing.T) {
	f := newBatcherFixture(t, 50, false)
	lids := lidsOf(t, 3)

	for _, l := range lids {
		f.batcher.Enqueue(l)
	}
	f.batcher.Remove(lids[1])
	require.Equal(t, 2, f.batcher.Pending())

	require.True(t, f.batcher.Flush(context.Background()))
	msgs := f.waitCaptured()
	require.Len(t, msgs, 2)
	require.Equal(t, lids[0], msgs[0].(*mvbeta.MsgWithdrawLease).ID)
	require.Equal(t, lids[2], msgs[1].(*mvbeta.MsgWithdrawLease).ID)
}

// Remove on an in-flight id is a no-op; Remove on a still-pending id drops it
// from the next batch.
func TestWithdrawBatcher_RemoveAfterFlushDoesNotAffectBatch(t *testing.T) {
	f := newBatcherFixture(t, 2, true)
	lids := lidsOf(t, 3)

	for _, l := range lids {
		f.batcher.Enqueue(l)
	}
	require.True(t, f.batcher.Flush(context.Background()))

	first := f.waitCaptured()
	require.Len(t, first, 2)
	require.Equal(t, lids[0], first[0].(*mvbeta.MsgWithdrawLease).ID)
	require.Equal(t, lids[1], first[1].(*mvbeta.MsgWithdrawLease).ID)

	f.batcher.Remove(lids[0])
	f.batcher.Remove(lids[2])

	close(f.release)
	require.NoError(t, f.waitDone())
	f.batcher.MarkDone()

	require.False(t, f.batcher.Flush(context.Background()))
	require.Equal(t, 0, f.batcher.Pending())
}

// Re-enqueueing a still-pending lease is a no-op, so a lease that fires its
// timer again while a prior trigger is still queued does not duplicate the msg.
func TestWithdrawBatcher_EnqueueDedupes(t *testing.T) {
	f := newBatcherFixture(t, 50, false)
	lid := testutil.LeaseID(t)

	f.batcher.Enqueue(lid)
	f.batcher.Enqueue(lid)
	f.batcher.Enqueue(lid)
	require.Equal(t, 1, f.batcher.Pending())

	require.True(t, f.batcher.Flush(context.Background()))
	msgs := f.waitCaptured()
	require.Len(t, msgs, 1)
}

func TestWithdrawBatcher_EnqueueDedupesAfterFailure(t *testing.T) {
	wantErr := errors.New("broadcast failed")

	tx := &clientmocks.TxClient{}
	captured := make(chan []sdk.Msg, 2)
	release := make(chan struct{})

	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured <- args.Get(1).([]sdk.Msg)
			<-release
		}).
		Return(nil, wantErr).Once()

	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			captured <- args.Get(1).([]sdk.Msg)
		}).
		Return(&sdk.TxResponse{}, nil).Once()

	b := newWithdrawBatcher(tx, testLogger(), time.Second, 50)
	lidA := testutil.LeaseID(t)
	lidB := testutil.LeaseID(t)

	b.Enqueue(lidA)
	require.True(t, b.Flush(context.Background()))

	first := <-captured
	require.Len(t, first, 1)
	require.Equal(t, lidA, first[0].(*mvbeta.MsgWithdrawLease).ID)

	b.Enqueue(lidB)
	require.Equal(t, 1, b.Pending())

	close(release)
	require.ErrorIs(t, <-b.Done(), wantErr)
	b.MarkDone()

	b.Enqueue(lidB)
	require.Equal(t, 1, b.Pending(), "dedup must prevent stale pending entry from duplicating")

	require.True(t, b.Flush(context.Background()))
	second := <-captured
	require.Len(t, second, 1)
	require.Equal(t, lidB, second[0].(*mvbeta.MsgWithdrawLease).ID)

	require.NoError(t, <-b.Done())
}

func TestWithdrawBatcher_ErrorPropagatesToDone(t *testing.T) {
	wantErr := errors.New("broadcast failed")

	tx := &clientmocks.TxClient{}
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, wantErr)

	b := newWithdrawBatcher(tx, testLogger(), time.Second, 50)
	b.Enqueue(testutil.LeaseID(t))
	require.True(t, b.Flush(context.Background()))

	err := <-b.Done()
	require.ErrorIs(t, err, wantErr)
}

func TestWithdrawBatcher_FlushEmptyNoop(t *testing.T) {
	f := newBatcherFixture(t, 50, false)
	require.False(t, f.batcher.Flush(context.Background()))
	f.tx.AssertNotCalled(t, "BroadcastMsgs")
}

func TestWithdrawBatcher_TimeoutAppliedPerCall(t *testing.T) {
	tx := &clientmocks.TxClient{}
	tx.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(50*time.Millisecond), deadline, 50*time.Millisecond)
			<-ctx.Done()
		}).
		Return(nil, context.DeadlineExceeded)

	b := newWithdrawBatcher(tx, testLogger(), 50*time.Millisecond, 50)
	b.Enqueue(testutil.LeaseID(t))
	require.True(t, b.Flush(context.Background()))
	err := <-b.Done()
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
