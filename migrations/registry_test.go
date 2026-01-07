package migrations

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type testMigration struct {
	name        string
	description string
	fromVersion string
	runError    error
}

func (m *testMigration) Name() string {
	return m.name
}

func (m *testMigration) Description() string {
	return m.description
}

func (m *testMigration) FromVersion() string {
	return m.fromVersion
}

func (m *testMigration) Run(ctx context.Context) error {
	return m.runError
}

func TestRegister(t *testing.T) {
	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	m := &testMigration{
		name:        "test-migration",
		description: "test description",
		fromVersion: "1.0.0",
	}

	Register(m)

	all := GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 migration, got %d", len(all))
	}
	if all[0] != m {
		t.Error("expected registered migration to match")
	}
}

func TestRegister_DuplicateName(t *testing.T) {
	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	m1 := &testMigration{name: "test-migration"}
	m2 := &testMigration{name: "test-migration"}

	Register(m1)
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		Register(m2)
	}()

	if !panicked {
		t.Error("expected panic when registering duplicate name")
	}
}

func TestRegister_NilMigration(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		Register(nil)
	}()

	if !panicked {
		t.Error("expected panic when registering nil migration")
	}
}

func TestRegister_EmptyName(t *testing.T) {
	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	m := &testMigration{name: ""}

	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		Register(m)
	}()

	if !panicked {
		t.Error("expected panic when registering migration with empty name")
	}
}

func TestGetAll_Sorted(t *testing.T) {
	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	m1 := &testMigration{name: "z-migration"}
	m2 := &testMigration{name: "a-migration"}
	m3 := &testMigration{name: "m-migration"}

	Register(m1)
	Register(m2)
	Register(m3)

	all := GetAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(all))
	}
	if all[0].Name() != "a-migration" {
		t.Errorf("expected first migration to be 'a-migration', got %q", all[0].Name())
	}
	if all[1].Name() != "m-migration" {
		t.Errorf("expected second migration to be 'm-migration', got %q", all[1].Name())
	}
	if all[2].Name() != "z-migration" {
		t.Errorf("expected third migration to be 'z-migration', got %q", all[2].Name())
	}
}

func TestGet(t *testing.T) {
	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	m := &testMigration{name: "test-migration"}
	Register(m)

	found := Get("test-migration")
	if found != m {
		t.Errorf("expected to find migration, got %v", found)
	}

	notFound := Get("non-existent")
	if notFound != nil {
		t.Errorf("expected nil for non-existent migration, got %v", notFound)
	}
}

func TestRegistry_GetPending_AllApplied(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		fromVersion: "1.0.0",
	}
	Register(m)

	err := sm.MarkApplied("test-migration")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	err = sm.SetProviderVersion("1.1.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.2.0")
	pending, err := reg.GetPending(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending migrations, got %d", len(pending))
	}
}

func TestRegistry_GetPending_NoneApplied(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		fromVersion: "1.0.0",
	}
	Register(m)

	err := sm.SetProviderVersion("0.9.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.1.0")
	pending, err := reg.GetPending(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending migration, got %d", len(pending))
	}
	if pending[0] != m {
		t.Error("expected pending migration to match")
	}
}

func TestRegistry_GetPending_Mixed(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m1 := &testMigration{
		name:        "applied-migration",
		fromVersion: "1.0.0",
	}
	m2 := &testMigration{
		name:        "pending-migration",
		fromVersion: "1.0.0",
	}
	m3 := &testMigration{
		name:        "state-applied-migration",
		fromVersion: "1.0.0",
	}

	Register(m1)
	Register(m2)
	Register(m3)

	err := sm.MarkApplied("applied-migration")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	err = sm.MarkApplied("state-applied-migration")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	err = sm.SetProviderVersion("0.9.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.1.0")
	pending, err := reg.GetPending(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending migration, got %d", len(pending))
	}
	if pending[0] != m2 {
		t.Error("expected pending migration to be m2")
	}
}

// TestRegistry_GetPending_IsAppliedError is no longer relevant since
// we no longer call IsApplied() in GetPending. Migrations are tracked
// solely via the state file based on version history.

func TestRegistry_RunMigrations_Success(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		runError:    nil,
		fromVersion: "1.0.0",
	}
	Register(m)

	err := sm.SetProviderVersion("0.9.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.1.0")
	successCount, errs := reg.RunMigrations(context.Background())

	if successCount != 1 {
		t.Errorf("expected 1 successful migration, got %d", successCount)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d", len(errs))
	}

	applied, err := sm.IsApplied("test-migration")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !applied {
		t.Error("expected migration to be marked as applied")
	}
}

func TestRegistry_RunMigrations_RunError(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		runError:    errors.New("run failed"),
		fromVersion: "1.0.0",
	}
	Register(m)

	err := sm.SetProviderVersion("0.9.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.1.0")
	successCount, errs := reg.RunMigrations(context.Background())

	if successCount != 0 {
		t.Errorf("expected 0 successful migrations, got %d", successCount)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "migration \"test-migration\" failed") {
		t.Errorf("expected error to contain 'migration \"test-migration\" failed', got %v", errs[0])
	}

	applied, err := sm.IsApplied("test-migration")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if applied {
		t.Error("expected migration to not be marked as applied")
	}
}

func TestRegistry_RunMigrations_MultipleMigrations(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m1 := &testMigration{
		name:        "migration1",
		runError:    nil,
		fromVersion: "1.0.0",
	}
	m2 := &testMigration{
		name:        "migration2",
		runError:    errors.New("failed"),
		fromVersion: "1.0.0",
	}
	m3 := &testMigration{
		name:        "migration3",
		runError:    nil,
		fromVersion: "1.0.0",
	}

	Register(m1)
	Register(m2)
	Register(m3)

	err := sm.SetProviderVersion("0.9.0")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	reg.SetCurrentVersion("1.1.0")
	successCount, errs := reg.RunMigrations(context.Background())

	if successCount != 2 {
		t.Errorf("expected 2 successful migrations, got %d", successCount)
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}

	applied, err := sm.IsApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !applied {
		t.Error("expected migration1 to be marked as applied")
	}

	applied, err = sm.IsApplied("migration2")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if applied {
		t.Error("expected migration2 to not be marked as applied")
	}

	applied, err = sm.IsApplied("migration3")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !applied {
		t.Error("expected migration3 to be marked as applied")
	}
}

func TestRegistry_GetPending_FreshInstall(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		fromVersion: "1.0.0",
	}
	Register(m)

	reg.SetCurrentVersion("1.1.0")

	pending, err := reg.GetPending(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected no pending migrations on fresh install, got %d", len(pending))
	}

	version, err := sm.GetProviderVersion()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if version != "" {
		t.Errorf("expected empty version on fresh install, got %q", version)
	}
}

func TestRegistry_RunMigrations_FreshInstall(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := tmpDir + "/migrations.json"

	originalRegistry := registry
	defer func() {
		registry = originalRegistry
	}()

	registry = make(map[string]Migration)

	sm := NewStateManager(statePath)
	reg := NewRegistry(sm)

	m := &testMigration{
		name:        "test-migration",
		runError:    nil,
		fromVersion: "1.0.0",
	}
	Register(m)

	reg.SetCurrentVersion("1.1.0")

	successCount, errs := reg.RunMigrations(context.Background())

	if successCount != 0 {
		t.Errorf("expected 0 successful migrations on fresh install, got %d", successCount)
	}
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d", len(errs))
	}

	version, err := sm.GetProviderVersion()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if version != "1.1.0" {
		t.Errorf("expected version to be set to 1.1.0, got %q", version)
	}
}

func TestVersionLessThan(t *testing.T) {
	tests := []struct {
		name string
		v1   string
		v2   string
		want bool
	}{
		{"v1 less than v2", "1.0.0", "1.1.0", true},
		{"v1 greater than v2", "1.1.0", "1.0.0", false},
		{"v1 equal to v2", "1.1.0", "1.1.0", false},
		{"with v prefix", "v1.0.0", "v1.1.0", true},
		{"different major", "0.9.0", "1.0.0", true},
		{"different patch", "1.1.0", "1.1.1", true},
		{"empty v1", "", "1.0.0", false},
		{"empty v2", "1.0.0", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := versionLessThan(tt.v1, tt.v2)
			if got != tt.want {
				t.Errorf("versionLessThan(%q, %q) = %v, want %v", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}
