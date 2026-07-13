package migrations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStateManager_Load_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	sm := NewStateManager(statePath)
	state, err := sm.Load()

	require.NoError(t, err)
	require.NotNil(t, state)
	require.Empty(t, state.Applied)
	require.True(t, state.LastRun.IsZero())
}

func TestStateManager_Load_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	expectedState := &State{
		Applied: []string{"migration1", "migration2"},
		LastRun: time.Now(),
	}

	data, err := json.MarshalIndent(expectedState, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal state: %v", err)
	}

	// nolint:gosec
	err = os.WriteFile(statePath, data, 0644)
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	sm := NewStateManager(statePath)
	state, err := sm.Load()

	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, len(expectedState.Applied), len(state.Applied))
	for i, v := range expectedState.Applied {
		require.Equal(t, v, state.Applied[i], "Applied[%d]", i)
	}
	require.InDelta(t, 0, state.LastRun.Sub(expectedState.LastRun).Seconds(), 1.0,
		"LastRun should be within 1 second")
}

func TestStateManager_Load_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	// nolint:gosec
	err := os.WriteFile(statePath, []byte("invalid json"), 0644)
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	sm := NewStateManager(statePath)
	state, err := sm.Load()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if state != nil {
		t.Errorf("expected nil state, got %v", state)
	}
	if !strings.Contains(err.Error(), "unable to unmarshal state") {
		t.Errorf("expected error to contain 'unable to unmarshal state', got %v", err)
	}
}

func TestStateManager_Save(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	sm := NewStateManager(statePath)
	state := &State{
		Applied: []string{"migration1", "migration2"},
		LastRun: time.Time{},
	}

	err := sm.Save(state)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	loadedState, err := sm.Load()
	if err != nil {
		t.Fatalf("expected no error loading, got %v", err)
	}
	if len(loadedState.Applied) != len(state.Applied) {
		t.Errorf("expected Applied length %d, got %d", len(state.Applied), len(loadedState.Applied))
	}
	for i, v := range state.Applied {
		if i >= len(loadedState.Applied) || loadedState.Applied[i] != v {
			t.Errorf("expected Applied[%d] = %q, got %q", i, v, loadedState.Applied[i])
		}
	}
	if loadedState.LastRun.IsZero() {
		t.Error("expected LastRun to be set, got zero time")
	}
}

func TestStateManager_Save_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nested", "dir", "migrations.json")

	sm := NewStateManager(statePath)
	state := &State{
		Applied: []string{"migration1"},
	}

	err := sm.Save(state)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err = os.Stat(statePath)
	if err != nil {
		t.Fatalf("expected file to exist, got error: %v", err)
	}
}

func TestStateManager_IsApplied(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	sm := NewStateManager(statePath)

	applied, err := sm.IsApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if applied {
		t.Error("expected migration1 to not be applied")
	}

	err = sm.MarkApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	applied, err = sm.IsApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !applied {
		t.Error("expected migration1 to be applied")
	}

	applied, err = sm.IsApplied("migration2")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if applied {
		t.Error("expected migration2 to not be applied")
	}
}

func TestStateManager_MarkApplied(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	sm := NewStateManager(statePath)

	err := sm.MarkApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	err = sm.MarkApplied("migration2")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	state, err := sm.Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found1 := false
	found2 := false
	for _, name := range state.Applied {
		if name == "migration1" {
			found1 = true
		}
		if name == "migration2" {
			found2 = true
		}
	}
	if !found1 {
		t.Error("expected migration1 to be in Applied list")
	}
	if !found2 {
		t.Error("expected migration2 to be in Applied list")
	}
	if len(state.Applied) != 2 {
		t.Errorf("expected 2 applied migrations, got %d", len(state.Applied))
	}
}

func TestStateManager_MarkApplied_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "migrations.json")

	sm := NewStateManager(statePath)

	err := sm.MarkApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	err = sm.MarkApplied("migration1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	state, err := sm.Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found := false
	for _, name := range state.Applied {
		if name == "migration1" {
			found = true
		}
	}
	if !found {
		t.Error("expected migration1 to be in Applied list")
	}
	if len(state.Applied) != 1 {
		t.Errorf("expected 1 applied migration, got %d", len(state.Applied))
	}
}
