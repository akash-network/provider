package cmd

import (
	"context"
	"errors"
	"fmt"

	"pkg.akt.dev/go/util/events"
)

// errEventsStopped is what the provider stops with when the chain event service stops
// while the provider is still running.
var errEventsStopped = errors.New("chain events service stopped")

// watchEvents waits until ctx ends. If the chain event service stops first, on its own,
// it returns errEventsStopped with the service's reason, so the provider stops rather
// than keep bidding and serving leases without chain events.
func watchEvents(ctx context.Context, svc events.Service) error {
	select {
	case <-ctx.Done():
		return nil
	case <-svc.Done():
	}

	if ctx.Err() != nil {
		return nil
	}

	if err := svc.Err(); err != nil {
		return fmt.Errorf("%w: %w", errEventsStopped, err)
	}

	return errEventsStopped
}
