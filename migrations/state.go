package migrations

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State represents the state of executed migrations.
type State struct {
	Applied         []string  `json:"applied"`
	LastRun         time.Time `json:"last_run"`
	ProviderVersion string    `json:"provider_version,omitempty"`
}

// StateManager handles reading and writing migration state to a file.
type StateManager struct {
	statePath string
}

// NewStateManager creates a new StateManager with the given state file path.
func NewStateManager(statePath string) *StateManager {
	return &StateManager{
		statePath: statePath,
	}
}

// Load reads the migration state from the file.
// If the file doesn't exist, returns an empty state.
func (sm *StateManager) Load() (*State, error) {
	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{
				Applied:         []string{},
				LastRun:         time.Time{},
				ProviderVersion: "",
			}, nil
		}
		return nil, fmt.Errorf("could not read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unable to unmarshal state: %w", err)
	}

	return &state, nil
}

// Save writes the migration state to the file.
// Creates the directory if it doesn't exist.
func (sm *StateManager) Save(state *State) error {
	state.LastRun = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to marshal state: %w", err)
	}

	dir := filepath.Dir(sm.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create state directory: %w", err)
	}

	if err := os.WriteFile(sm.statePath, data, 0600); err != nil {
		return fmt.Errorf("could not write state file: %w", err)
	}

	return nil
}

// IsApplied checks if a migration with the given name has been applied.
func (sm *StateManager) IsApplied(name string) (bool, error) {
	state, err := sm.Load()
	if err != nil {
		return false, err
	}

	for _, applied := range state.Applied {
		if applied == name {
			return true, nil
		}
	}

	return false, nil
}

// MarkApplied adds a migration name to the applied list and saves the state.
func (sm *StateManager) MarkApplied(name string) error {
	state, err := sm.Load()
	if err != nil {
		return err
	}

	for _, applied := range state.Applied {
		if applied == name {
			return nil
		}
	}

	state.Applied = append(state.Applied, name)
	return sm.Save(state)
}

// SetProviderVersion sets the provider version in the state.
func (sm *StateManager) SetProviderVersion(version string) error {
	state, err := sm.Load()
	if err != nil {
		return err
	}

	state.ProviderVersion = version
	return sm.Save(state)
}

// GetProviderVersion returns the provider version from the state.
func (sm *StateManager) GetProviderVersion() (string, error) {
	state, err := sm.Load()
	if err != nil {
		return "", err
	}

	return state.ProviderVersion, nil
}
