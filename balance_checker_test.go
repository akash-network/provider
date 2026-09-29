package provider

import (
	"context"
	"testing"
	"time"

	"cosmossdk.io/log"
	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/require"

	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	"github.com/akash-network/provider/event"
)

type balanceTestSubscriber struct {
	pubsub.Subscriber
	events chan pubsub.Event
}

func (s *balanceTestSubscriber) Events() <-chan pubsub.Event { return s.events }
func (s *balanceTestSubscriber) Close()                      {}

type balanceTestBus struct {
	pubsub.Bus
	subscriber *balanceTestSubscriber
}

func (b *balanceTestBus) Subscribe() (pubsub.Subscriber, error) { return b.subscriber, nil }

func TestBalanceCheckerExpiredTimer(t *testing.T) {
	for _, remove := range []bool{true, false} {
		name := "shutdown"
		if remove {
			name = "remove monitor"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lid := testutil.LeaseID(t)
			fired := make(chan struct{})
			timer := time.AfterFunc(0, func() { close(fired) })
			<-fired
			subscriber := &balanceTestSubscriber{events: make(chan pubsub.Event)}
			bc := &balanceChecker{
				ctx: ctx, lc: lifecycle.New(), log: log.NewNopLogger(),
				bus:    &balanceTestBus{subscriber: subscriber},
				leases: map[mtypes.LeaseID]*leaseState{lid: {tm: timer}},
			}
			started, finished := make(chan error, 1), make(chan struct{})
			go func() {
				bc.run(started)
				close(finished)
			}()
			require.NoError(t, <-started)
			if remove {
				subscriber.events <- event.LeaseRemoveFundsMonitor{LeaseID: lid}
				// Processing another event proves monitor removal did not hang.
				select {
				case subscriber.events <- struct{}{}:
				case <-time.After(time.Second):
					t.Fatal("removing a monitor with an expired timer blocked the balance checker")
				}
			}
			bc.lc.ShutdownAsync(nil)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("balance checker shutdown blocked on an expired timer")
			}
		})
	}
}
