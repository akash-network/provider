package waiter

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/akash-network/node/testutil"
	"github.com/stretchr/testify/require"
)

type fakeWaiter struct {
	failure error
}

func (fw fakeWaiter) Check(_ context.Context) error {
	return fw.failure
}

func (fw fakeWaiter) String() string {
	return "fakeWaiter"
}

func TestWaiterNoInput(t *testing.T) {
	logger := testutil.Logger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// no objects passed
	waiter := NewOperatorWaiter(ctx, logger)
	require.NotNil(t, waiter)
	require.NoError(t, waiter.WaitForAll(ctx))
}

func TestWaiterContextCancelled(t *testing.T) {
	logger := testutil.Logger(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitable := fakeWaiter{failure: fmt.Errorf("test error")}

	waiter := NewOperatorWaiter(ctx, logger, &waitable)
	require.NotNil(t, waiter)
	require.ErrorIs(t, waiter.WaitForAll(ctx), context.Canceled)
}

func TestWaiterInputReady(t *testing.T) {
	waitable := fakeWaiter{failure: nil}
	logger := testutil.Logger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	waiter := NewOperatorWaiter(ctx, logger, waitable)
	require.NotNil(t, waiter)
	require.NoError(t, waiter.WaitForAll(ctx))
}

func TestWaiterInputFailed(t *testing.T) {
	waitable := fakeWaiter{failure: io.EOF}
	logger := testutil.Logger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	waiter := NewOperatorWaiter(ctx, logger, waitable)
	require.NotNil(t, waiter)
	require.ErrorIs(t, waiter.WaitForAll(ctx), context.DeadlineExceeded)
}
