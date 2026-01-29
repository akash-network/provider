package migrations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	registry     = make(map[string]Migration)
	registryLock sync.RWMutex
)

// Register adds a migration to the registry.
// This should be called from init() functions in migration files.
// Panics if an error occurs.
func Register(m Migration) {
	if m == nil {
		panic("cannot register nil migration")
	}

	name := m.Name()
	if name == "" {
		panic("migration name cannot be empty")
	}

	registryLock.Lock()
	defer registryLock.Unlock()

	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("migration with name %q already registered", name))
	}

	registry[name] = m
}

// GetAll returns all registered migrations.
func GetAll() []Migration {
	registryLock.RLock()
	defer registryLock.RUnlock()

	migrations := make([]Migration, 0, len(registry))
	for _, m := range registry {
		migrations = append(migrations, m)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Name() < migrations[j].Name()
	})

	return migrations
}

// Get returns a migration by name, or nil if not found.
func Get(name string) Migration {
	registryLock.RLock()
	defer registryLock.RUnlock()

	return registry[name]
}

// Registry manages migration discovery and execution.
type Registry struct {
	stateManager   *StateManager
	currentVersion string
}

// NewRegistry creates a new migration registry.
func NewRegistry(stateManager *StateManager) *Registry {
	return &Registry{
		stateManager: stateManager,
	}
}

// SetCurrentVersion sets the current provider version.
// This should be called before GetPending or RunMigrations.
func (r *Registry) SetCurrentVersion(version string) {
	r.currentVersion = version
}

// GetPending returns all migrations that have not been applied yet.
// Migrations are only included if:
// 1. They haven't been applied (not in state file)
// 2. The previous provider version is less than or equal to the migration's FromVersion
// 3. For fresh installs (no previous version), all migrations are marked as applied and skipped
func (r *Registry) GetPending(ctx context.Context) ([]Migration, error) {
	all := GetAll()
	pending := make([]Migration, 0)

	previousVersion, err := r.stateManager.GetProviderVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get previous provider version: %w", err)
	}

	isFreshInstall := previousVersion == ""

	for _, m := range all {
		applied, err := r.stateManager.IsApplied(m.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to check if migration %q is applied: %w", m.Name(), err)
		}

		if applied {
			continue
		}

		fromVersion := m.FromVersion()
		if fromVersion == "" {
			continue
		}

		if isFreshInstall {
			continue
		}

		// Check if migration should run based on version
		// Migrations run if: previousVersion <= fromVersion
		normalizedPrevious := strings.TrimPrefix(previousVersion, "v")
		normalizedFrom := strings.TrimPrefix(fromVersion, "v")

		versionLess := versionLessThan(normalizedPrevious, normalizedFrom)
		versionEqual := normalizedPrevious == normalizedFrom
		versionMatches := versionLess || versionEqual

		if !versionMatches {
			continue
		}

		pending = append(pending, m)
	}

	return pending, nil
}

// RunMigrations executes all pending migrations.
// Returns the number of migrations that were successfully executed.
// After successful execution, updates the provider version and applied migrations in state.
func (r *Registry) RunMigrations(ctx context.Context) (int, []error) {
	pending, err := r.GetPending(ctx)
	if err != nil {
		return 0, []error{err}
	}

	var errors []error
	successCount := 0

	for _, m := range pending {
		if err := m.Run(ctx); err != nil {
			errors = append(errors, fmt.Errorf("migration %q failed: %w", m.Name(), err))
			continue
		}

		if err := r.stateManager.MarkApplied(m.Name()); err != nil {
			errors = append(errors, fmt.Errorf("failed to mark migration %q as applied: %w", m.Name(), err))
			continue
		}

		successCount++
	}

	if r.currentVersion != "" {
		if err := r.stateManager.SetProviderVersion(r.currentVersion); err != nil {
			errors = append(errors, fmt.Errorf("failed to update provider version: %w", err))
		}
	}

	return successCount, errors
}

// versionLessThan compares two version strings.
// Returns true if v1 < v2.
// Uses simple string comparison for versions like "1.1.0", "1.2.0", etc.
// For more complex semver, this could be enhanced.
// Suffix components (e.g. "-rc1") are ignored.
func versionLessThan(v1, v2 string) bool {
	if v1 == "" {
		return false
	}
	if v2 == "" {
		return false
	}

	v1 = stripVersionSuffix(v1)
	v2 = stripVersionSuffix(v2)

	v1Parts := strings.Split(strings.TrimPrefix(v1, "v"), ".")
	v2Parts := strings.Split(strings.TrimPrefix(v2, "v"), ".")

	maxLen := len(v1Parts)
	if len(v2Parts) > maxLen {
		maxLen = len(v2Parts)
	}

	for i := 0; i < maxLen; i++ {
		v1Part := parseVersionPart(v1Parts, i)
		v2Part := parseVersionPart(v2Parts, i)

		if v1Part < v2Part {
			return true
		}
		if v1Part > v2Part {
			return false
		}
	}

	return false
}

// parseVersionPart parses a version part at the given index.
// Returns 0 if the index is out of bounds.
func parseVersionPart(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	var result int
	part := stripVersionSuffix(parts[index])
	fmt.Sscanf(part, "%d", &result)
	return result
}

// stripVersionSuffix removes suffix components (e.g., "-rc1") from a version string or part.
func stripVersionSuffix(s string) string {
	// Remove everything after "-"
	if idx := strings.IndexAny(s, "-"); idx != -1 {
		return s[:idx]
	}
	return s
}
