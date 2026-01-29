package migrations

import (
	"context"
)

// Migration represents a single migration that can be executed.
// All migrations must be idempotent, running them multiple times
// should have the same effect as running them once.
type Migration interface {
	// Name returns a unique identifier for this migration.
	// This identifier is used to track whether the migration has been applied.
	Name() string

	// Description returns a human-readable description of what this migration does.
	Description() string

	// FromVersion returns the provider version this migration applies from.
	// Migrations will only run when upgrading from a version < FromVersion.
	// For fresh installs (no previous version), migrations are skipped.
	// Return empty string to indicate this migration should always run (not recommended).
	FromVersion() string

	// Run executes the migration. This method must be idempotent.
	// If the migration has already been applied, this should be a no-op.
	// The context provides access to Kubernetes clients and other resources.
	// The registry tracks applied migrations via the state file, so Run()
	// should be safe to call multiple times.
	Run(ctx context.Context) error
}
