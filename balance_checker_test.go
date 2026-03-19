package provider

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/util/pubsub"

	"github.com/akash-network/provider/session"
)

func TestNewBalanceCheckerInitializesWithdrawSemaphore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := pubsub.NewBus()
	sess := session.New(log.NewLogger(io.Discard), nil, nil, 0)

	bc, err := newBalanceChecker(ctx, nil, sdk.AccAddress{}, sess, bus, BalanceCheckerConfig{})
	require.NoError(t, err)
	require.NotNil(t, bc.withdrawSem)
	require.Equal(t, maxConcurrentWithdraws, cap(bc.withdrawSem))

	require.NoError(t, bc.Close())
}

func TestBalanceCheckerWithdrawSemaphoreCapacity(t *testing.T) {
	bc := &balanceChecker{
		withdrawSem: make(chan struct{}, maxConcurrentWithdraws),
	}

	for i := 0; i < maxConcurrentWithdraws; i++ {
		select {
		case bc.withdrawSem <- struct{}{}:
		default:
			t.Fatalf("failed to acquire slot %d", i)
		}
	}

	select {
	case bc.withdrawSem <- struct{}{}:
		t.Fatal("expected semaphore to be full")
	default:
	}

	<-bc.withdrawSem

	select {
	case bc.withdrawSem <- struct{}{}:
	default:
		t.Fatal("expected to acquire semaphore slot after release")
	}
}

func TestBalanceCheckerSixthRequestWaitsForCompletion(t *testing.T) {
	bc := &balanceChecker{
		withdrawSem: make(chan struct{}, maxConcurrentWithdraws),
	}

	const totalRequests = maxConcurrentWithdraws + 1

	type acquireEvent struct {
		id int
	}

	acquired := make(chan acquireEvent, totalRequests)
	allowFinish := make([]chan struct{}, totalRequests)
	for i := range allowFinish {
		allowFinish[i] = make(chan struct{})
	}

	var wg sync.WaitGroup
	wg.Add(totalRequests)

	for i := 0; i < totalRequests; i++ {
		id := i
		go func() {
			defer wg.Done()
			bc.withdrawSem <- struct{}{}
			acquired <- acquireEvent{id: id}
			<-allowFinish[id]
			<-bc.withdrawSem
		}()
	}

	firstWave := make(map[int]struct{}, maxConcurrentWithdraws)
	for i := 0; i < maxConcurrentWithdraws; i++ {
		select {
		case ev := <-acquired:
			firstWave[ev.id] = struct{}{}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first wave to acquire semaphore")
		}
	}

	// The sixth request must still be blocked before any of the first five complete.
	select {
	case ev := <-acquired:
		t.Fatalf("request %d acquired before a slot was released", ev.id)
	case <-time.After(150 * time.Millisecond):
	}

	// Release one request from the first wave and verify exactly one waiting request can proceed.
	var releasedID int
	for id := range firstWave {
		releasedID = id
		break
	}
	close(allowFinish[releasedID])

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocked request to acquire after release")
	}

	for i := 0; i < totalRequests; i++ {
		if i == releasedID {
			continue
		}
		close(allowFinish[i])
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for all requests to finish")
	}
}
