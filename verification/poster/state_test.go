package poster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	verificationv1 "pkg.akt.dev/go/node/verification/v1"

	"github.com/akash-network/provider/tools/pconfig/memory"
)

func TestPersistentStateStore(t *testing.T) {
	ctx := context.Background()
	storage, err := memory.NewMemory()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, storage.Close())
	}()

	store, err := NewPersistentStateStore(storage.Verification())
	require.NoError(t, err)

	_, err = store.Get(ctx)
	require.ErrorIs(t, err, ErrStateNotFound)

	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	state := State{
		Provider:          "akash1provider",
		SnapshotHash:      []byte("hash"),
		SnapshotTimestamp: now,
		ResourceSummary: verificationv1.ResourceSummary{
			TotalGPUs:       2,
			TotalVCPUs:      16,
			TotalMemoryMB:   64 * 1024,
			TotalStorageMB:  1024 * 1024,
			ActiveLeases:    5,
			SoftwareVersion: "v1.2.3",
		},
		ComplianceDeadline: now.Add(time.Hour),
		LastDecision:       string(DecisionReasonMissingRecord),
		LastTxHash:         "snapshot-tx",
		LastAttemptAt:      now,
		LastSuccessAt:      now,
	}
	require.NoError(t, store.Set(ctx, state))

	stored, err := store.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, StateVersion, stored.Version)
	require.Equal(t, HashDomainSnapshotV1Full, stored.HashDomain)
	require.Equal(t, state.Provider, stored.Provider)
	require.Equal(t, state.SnapshotHash, stored.SnapshotHash)
	require.Equal(t, state.ResourceSummary, stored.ResourceSummary)
	require.Equal(t, state.LastTxHash, stored.LastTxHash)
}

func TestNewPersistentStateStoreValidatesStore(t *testing.T) {
	store, err := NewPersistentStateStore(nil)
	require.ErrorIs(t, err, errMissingState)
	require.Nil(t, store)
}

func TestPersistentStateStoreRejectsCorruptState(t *testing.T) {
	ctx := context.Background()
	storage, err := memory.NewMemory()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, storage.Close())
	}()

	require.NoError(t, storage.Verification().SetSnapshotPosterState(ctx, []byte("{")))

	store, err := NewPersistentStateStore(storage.Verification())
	require.NoError(t, err)

	_, err = store.Get(ctx)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrStateNotFound))
}
