package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/tools/pconfig/memory"
)

func TestNewSnapshotRecord(t *testing.T) {
	createdAt := time.Date(2026, 6, 15, 12, 0, 0, 123, time.FixedZone("test", -7*60*60))
	payload := testSnapshotPayload(t, "payload")
	expectedPayload := append([]byte(nil), payload...)
	snapshot := &Snapshot{
		Payload:   payload,
		Hash:      HashPayload(payload),
		Signature: []byte("signature"),
		Provider:  "akash1provider",
	}

	record, err := NewSnapshotRecord(snapshot, SnapshotPayloadSchemaVersion, createdAt)
	require.NoError(t, err)
	require.Equal(t, SnapshotPayloadSchemaVersion, record.SchemaVersion)
	require.Equal(t, createdAt.UTC(), record.CreatedAt)
	require.Equal(t, *snapshot, record.Snapshot)
	require.Equal(t, SnapshotValidationStatusValid, record.Validation.Status)
	require.Equal(t, createdAt.UTC(), record.Validation.ValidatedAt)

	snapshot.Payload[0] ^= 1
	require.Equal(t, expectedPayload, record.Snapshot.Payload)
}

func TestNewSnapshotRecordValidatesSnapshot(t *testing.T) {
	record, err := NewSnapshotRecord(nil, SnapshotPayloadSchemaVersion, time.Time{})
	require.ErrorIs(t, err, errMissingSnapshot)
	require.Empty(t, record)
}

func TestValidateSnapshotRecordRejectsHashMismatch(t *testing.T) {
	payload := testSnapshotPayload(t, "payload")
	record := SnapshotRecord{
		Snapshot: Snapshot{
			Payload:   payload,
			Hash:      []byte("wrong"),
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		},
	}

	require.ErrorIs(t, ValidateSnapshotRecord(record), errSnapshotRecordHashMismatch)
}

func TestValidateSnapshotRecordRejectsProviderMismatch(t *testing.T) {
	payload := testSnapshotPayload(t, "payload")
	record := SnapshotRecord{
		Snapshot: Snapshot{
			Payload:   payload,
			Hash:      HashPayload(payload),
			Signature: []byte("signature"),
			Provider:  "akash1otherprovider",
		},
	}

	require.ErrorIs(t, ValidateSnapshotRecord(record), errSnapshotRecordProvider)
}

func TestMemorySnapshotStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySnapshotStore()
	first := testSnapshotRecord(t, "payload-1")
	second := testSnapshotRecord(t, "payload-2")

	_, ok, err := store.Latest(ctx, first.Snapshot.Provider)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.Put(ctx, first))
	require.NoError(t, store.Put(ctx, second))

	latest, ok, err := store.Latest(ctx, first.Snapshot.Provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, second, latest)

	stored, ok, err := store.Get(ctx, first.Snapshot.Provider, first.Snapshot.Hash)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first, stored)

	stored.Snapshot.Payload[0] ^= 1
	again, ok, err := store.Get(ctx, first.Snapshot.Provider, first.Snapshot.Hash)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first.Snapshot.Payload, again.Snapshot.Payload)
}

func TestPersistentSnapshotStore(t *testing.T) {
	ctx := context.Background()
	storage, err := memory.NewMemory()
	require.NoError(t, err)

	store, err := NewPersistentSnapshotStore(storage.Verification())
	require.NoError(t, err)

	record := testSnapshotRecord(t, "payload")

	_, ok, err := store.Latest(ctx, record.Snapshot.Provider)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.Put(ctx, record))

	latest, ok, err := store.Latest(ctx, record.Snapshot.Provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, record, latest)

	stored, ok, err := store.Get(ctx, record.Snapshot.Provider, record.Snapshot.Hash)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, record, stored)
}

func TestRecordingSnapshotter(t *testing.T) {
	ctx := context.Background()
	payload := testSnapshotPayload(t, "payload")
	snapshot := &Snapshot{
		Payload:   payload,
		Hash:      HashPayload(payload),
		Signature: []byte("signature"),
		Provider:  "akash1provider",
	}
	builder := &testSnapshotBuilder{snapshot: snapshot}
	store := NewMemorySnapshotStore()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	snapshotter, err := NewRecordingSnapshotter(builder, store, func() time.Time { return now })
	require.NoError(t, err)

	result, err := snapshotter.Build(ctx, SnapshotRequest{})
	require.NoError(t, err)
	require.Equal(t, snapshot, result)

	record, ok, err := store.Latest(ctx, snapshot.Provider)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, now, record.CreatedAt)
	require.Equal(t, snapshot.Payload, record.Snapshot.Payload)
	require.Equal(t, SnapshotValidationStatusValid, record.Validation.Status)
}

func TestRecordingSnapshotterReturnsStoreError(t *testing.T) {
	expected := errors.New("store failed")
	payload := testSnapshotPayload(t, "payload")
	snapshotter, err := NewRecordingSnapshotter(
		&testSnapshotBuilder{snapshot: &Snapshot{
			Payload:   payload,
			Hash:      HashPayload(payload),
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		}},
		testSnapshotStore{err: expected},
		time.Now,
	)
	require.NoError(t, err)

	result, err := snapshotter.Build(context.Background(), SnapshotRequest{})
	require.ErrorIs(t, err, expected)
	require.Nil(t, result)
}

type testSnapshotBuilder struct {
	snapshot *Snapshot
	err      error
}

func (b *testSnapshotBuilder) Build(context.Context, SnapshotRequest) (*Snapshot, error) {
	return b.snapshot, b.err
}

type testSnapshotStore struct {
	err error
}

func (s testSnapshotStore) Put(context.Context, SnapshotRecord) error {
	return s.err
}

func (s testSnapshotStore) Latest(context.Context, string) (SnapshotRecord, bool, error) {
	return SnapshotRecord{}, false, s.err
}

func (s testSnapshotStore) Get(context.Context, string, []byte) (SnapshotRecord, bool, error) {
	return SnapshotRecord{}, false, s.err
}

func testSnapshotRecord(t *testing.T, payload string) SnapshotRecord {
	t.Helper()

	data := testSnapshotPayload(t, payload)
	snapshot := &Snapshot{
		Payload:   data,
		Hash:      HashPayload(data),
		Signature: []byte("signature"),
		Provider:  "akash1provider",
	}
	record, err := NewSnapshotRecord(snapshot, SnapshotPayloadSchemaVersion, time.Unix(1, 0))
	require.NoError(t, err)

	return record
}

func testSnapshotPayload(t *testing.T, value string) []byte {
	t.Helper()

	data, err := MarshalDeterministic(&inventoryv1.SnapshotPayload{
		SchemaVersion: SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
		EvidenceSections: []inventoryv1.SnapshotEvidenceSection{
			{
				Name:    "test",
				Payload: []byte(value),
			},
		},
	})
	require.NoError(t, err)

	return data
}
