package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/util/pubsub"
)

// stubEventsService is an events.Service whose state the test sets directly.
type stubEventsService struct {
	done chan struct{}
	err  error
}

func (s *stubEventsService) Shutdown()             {}
func (s *stubEventsService) Done() <-chan struct{} { return s.done }
func (s *stubEventsService) Err() error            { return s.err }

func stoppedEventsService(err error) *stubEventsService {
	s := &stubEventsService{done: make(chan struct{}), err: err}
	close(s.done)
	return s
}

// A provider without chain events would keep bidding and serving leases blind, so the
// event service stopping on its own stops the provider, with the reason: a bus that
// refused an event is the one the service reports.
func TestWatchEventsFailsWhenEventServiceStops(t *testing.T) {
	err := watchEvents(context.Background(), stoppedEventsService(pubsub.ErrNotRunning))
	require.ErrorIs(t, err, errEventsStopped)
	require.ErrorIs(t, err, pubsub.ErrNotRunning)

	err = watchEvents(context.Background(), stoppedEventsService(nil))
	require.ErrorIs(t, err, errEventsStopped, "a stop without a reason is still a stop")
}

// The event service stopping because the provider is shutting down is not a failure.
func TestWatchEventsIgnoresShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, watchEvents(ctx, &stubEventsService{done: make(chan struct{})}))
	require.NoError(t, watchEvents(ctx, stoppedEventsService(nil)))
}
