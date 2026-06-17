package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/tools/pconfig"
)

var (
	errSnapshotRecordHashMismatch = errors.New("inventory snapshot record hash mismatch")
	errSnapshotRecordProvider     = errors.New("inventory snapshot record provider mismatch")
	errMissingSnapshotBuilder     = errors.New("missing inventory snapshot builder")
	errMissingSnapshotStore       = errors.New("missing inventory snapshot store")
	errMissingSnapshotStoreClock  = errors.New("missing inventory snapshot store clock")
)

type SnapshotValidationStatus string

const (
	SnapshotValidationStatusUnvalidated SnapshotValidationStatus = "unvalidated"
	SnapshotValidationStatusValid       SnapshotValidationStatus = "valid"
	SnapshotValidationStatusInvalid     SnapshotValidationStatus = "invalid"
)

type SnapshotValidation struct {
	Status      SnapshotValidationStatus `json:"status"`
	Error       string                   `json:"error,omitempty"`
	ValidatedAt time.Time                `json:"validated_at,omitempty"`
}

type SnapshotRecord struct {
	Snapshot      Snapshot           `json:"snapshot"`
	SchemaVersion uint32             `json:"schema_version"`
	CreatedAt     time.Time          `json:"created_at"`
	Validation    SnapshotValidation `json:"validation"`
}

type SnapshotStore interface {
	Put(context.Context, SnapshotRecord) error
	Latest(context.Context, string) (SnapshotRecord, bool, error)
	Get(context.Context, string, []byte) (SnapshotRecord, bool, error)
}

type SnapshotBuilder interface {
	Build(context.Context, SnapshotRequest) (*Snapshot, error)
}

type RecordingSnapshotter struct {
	next  SnapshotBuilder
	store SnapshotStore
	now   func() time.Time
}

type MemorySnapshotStore struct {
	lock    sync.RWMutex
	latest  map[string]string
	records map[string]map[string]SnapshotRecord
}

type PersistentSnapshotStore struct {
	store pconfig.Verification
}

func NewRecordingSnapshotter(next SnapshotBuilder, store SnapshotStore, now func() time.Time) (*RecordingSnapshotter, error) {
	if next == nil {
		return nil, errMissingSnapshotBuilder
	}
	if store == nil {
		return nil, errMissingSnapshotStore
	}
	if now == nil {
		return nil, errMissingSnapshotStoreClock
	}

	return &RecordingSnapshotter{
		next:  next,
		store: store,
		now:   now,
	}, nil
}

func (s *RecordingSnapshotter) Build(ctx context.Context, req SnapshotRequest) (*Snapshot, error) {
	snapshot, err := s.next.Build(ctx, req)
	if err != nil {
		return nil, err
	}

	record, err := NewSnapshotRecord(snapshot, SnapshotPayloadSchemaVersion, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.store.Put(ctx, record); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		latest:  make(map[string]string),
		records: make(map[string]map[string]SnapshotRecord),
	}
}

func NewPersistentSnapshotStore(store pconfig.Verification) (*PersistentSnapshotStore, error) {
	if store == nil {
		return nil, errMissingSnapshotStore
	}

	return &PersistentSnapshotStore{store: store}, nil
}

func (s *PersistentSnapshotStore) Put(ctx context.Context, record SnapshotRecord) error {
	if err := ValidateSnapshotRecord(record); err != nil {
		return err
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	return s.store.SetInventorySnapshot(ctx, record.Snapshot.Provider, record.Snapshot.Hash, data)
}

func (s *PersistentSnapshotStore) Latest(ctx context.Context, provider string) (SnapshotRecord, bool, error) {
	data, err := s.store.GetLatestInventorySnapshot(ctx, provider)
	if errors.Is(err, pconfig.ErrNotExists) {
		return SnapshotRecord{}, false, nil
	}
	if err != nil {
		return SnapshotRecord{}, false, err
	}

	record, err := decodeSnapshotRecord(data)
	if err != nil {
		return SnapshotRecord{}, false, err
	}

	return record, true, nil
}

func (s *PersistentSnapshotStore) Get(ctx context.Context, provider string, hash []byte) (SnapshotRecord, bool, error) {
	data, err := s.store.GetInventorySnapshot(ctx, provider, hash)
	if errors.Is(err, pconfig.ErrNotExists) {
		return SnapshotRecord{}, false, nil
	}
	if err != nil {
		return SnapshotRecord{}, false, err
	}

	record, err := decodeSnapshotRecord(data)
	if err != nil {
		return SnapshotRecord{}, false, err
	}

	return record, true, nil
}

func (s *MemorySnapshotStore) Put(_ context.Context, record SnapshotRecord) error {
	if err := ValidateSnapshotRecord(record); err != nil {
		return err
	}

	provider := record.Snapshot.Provider
	hash := string(record.Snapshot.Hash)

	s.lock.Lock()
	defer s.lock.Unlock()

	providerRecords := s.records[provider]
	if providerRecords == nil {
		providerRecords = make(map[string]SnapshotRecord)
		s.records[provider] = providerRecords
	}

	providerRecords[hash] = cloneSnapshotRecord(record)
	s.latest[provider] = hash

	return nil
}

func (s *MemorySnapshotStore) Latest(_ context.Context, provider string) (SnapshotRecord, bool, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	hash, ok := s.latest[provider]
	if !ok {
		return SnapshotRecord{}, false, nil
	}

	record, ok := s.records[provider][hash]
	if !ok {
		return SnapshotRecord{}, false, nil
	}

	return cloneSnapshotRecord(record), true, nil
}

func (s *MemorySnapshotStore) Get(_ context.Context, provider string, hash []byte) (SnapshotRecord, bool, error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	providerRecords := s.records[provider]
	if providerRecords == nil {
		return SnapshotRecord{}, false, nil
	}

	record, ok := providerRecords[string(hash)]
	if !ok {
		return SnapshotRecord{}, false, nil
	}

	return cloneSnapshotRecord(record), true, nil
}

func NewSnapshotRecord(snapshot *Snapshot, schemaVersion uint32, createdAt time.Time) (SnapshotRecord, error) {
	if err := ValidateSnapshot(snapshot); err != nil {
		return SnapshotRecord{}, err
	}

	record := SnapshotRecord{
		Snapshot:      cloneSnapshot(snapshot),
		SchemaVersion: schemaVersion,
		CreatedAt:     createdAt.UTC(),
		Validation: SnapshotValidation{
			Status:      SnapshotValidationStatusValid,
			ValidatedAt: createdAt.UTC(),
		},
	}
	if err := ValidateSnapshotRecord(record); err != nil {
		return SnapshotRecord{}, err
	}

	return record, nil
}

func ValidateSnapshotRecord(record SnapshotRecord) error {
	if err := ValidateSnapshot(&record.Snapshot); err != nil {
		return err
	}

	if !bytes.Equal(HashPayload(record.Snapshot.Payload), record.Snapshot.Hash) {
		return errSnapshotRecordHashMismatch
	}

	var payload inventoryv1.SnapshotPayload
	if err := payload.Unmarshal(record.Snapshot.Payload); err != nil {
		return err
	}

	if payload.Provider != "" && payload.Provider != record.Snapshot.Provider {
		return errSnapshotRecordProvider
	}

	if err := ValidateNonce(payload.Nonce); err != nil {
		return err
	}

	return nil
}

func decodeSnapshotRecord(data []byte) (SnapshotRecord, error) {
	var record SnapshotRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return SnapshotRecord{}, err
	}
	if record.Validation.Status == "" {
		record.Validation.Status = SnapshotValidationStatusUnvalidated
	}
	if err := ValidateSnapshotRecord(record); err != nil {
		return SnapshotRecord{}, err
	}

	return record, nil
}

func cloneSnapshot(snapshot *Snapshot) Snapshot {
	return Snapshot{
		Payload:   append([]byte(nil), snapshot.Payload...),
		Hash:      append([]byte(nil), snapshot.Hash...),
		Signature: append([]byte(nil), snapshot.Signature...),
		Provider:  snapshot.Provider,
	}
}

func cloneSnapshotRecord(record SnapshotRecord) SnapshotRecord {
	return SnapshotRecord{
		Snapshot:      cloneSnapshot(&record.Snapshot),
		SchemaVersion: record.SchemaVersion,
		CreatedAt:     record.CreatedAt,
		Validation:    record.Validation,
	}
}
