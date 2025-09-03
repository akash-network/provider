package ctx

import (
	"context"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
)

// contextKey is a private type for context keys to avoid collisions
type contextKey string

const (
	portManagerKey contextKey = "port-manager"
	leaseIDKey     contextKey = "lease-id"
)

// WithPortManager adds a PortManager to the context
func WithPortManager(ctx context.Context, pm interface{}) context.Context {
	return context.WithValue(ctx, portManagerKey, pm)
}

// PortManagerFrom retrieves a PortManager from the context
func PortManagerFrom(ctx context.Context) (interface{}, bool) {
	pm := ctx.Value(portManagerKey)
	if pm == nil {
		return nil, false
	}
	return pm, true
}

// WithLeaseID adds a LeaseID to the context
func WithLeaseID(ctx context.Context, leaseID mtypes.LeaseID) context.Context {
	return context.WithValue(ctx, leaseIDKey, leaseID)
}

// LeaseIDFrom retrieves a LeaseID from the context
func LeaseIDFrom(ctx context.Context) (mtypes.LeaseID, bool) {
	leaseID, ok := ctx.Value(leaseIDKey).(mtypes.LeaseID)
	return leaseID, ok
}
