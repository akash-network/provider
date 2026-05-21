package poster

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	verificationv1 "pkg.akt.dev/go/node/verification/v1"

	"github.com/akash-network/provider/tools/pconfig"
)

const (
	StateVersion             uint32 = 1
	HashDomainSnapshotV1Full        = "akash.inventory.snapshot.v1/full-payload/no-nonce"
)

var (
	ErrStateNotFound = errors.New("snapshot poster state not found")
	errMissingState  = errors.New("missing snapshot poster state store")
)

type State struct {
	Version            uint32                         `json:"version"`
	HashDomain         string                         `json:"hash_domain"`
	Provider           string                         `json:"provider"`
	SnapshotHash       []byte                         `json:"snapshot_hash,omitempty"`
	SnapshotTimestamp  time.Time                      `json:"snapshot_timestamp,omitempty"`
	ResourceSummary    verificationv1.ResourceSummary `json:"resource_summary"`
	ComplianceDeadline time.Time                      `json:"compliance_deadline,omitempty"`
	Suspended          bool                           `json:"suspended"`
	LastDecision       string                         `json:"last_decision,omitempty"`
	LastTxHash         string                         `json:"last_tx_hash,omitempty"`
	LastAttemptAt      time.Time                      `json:"last_attempt_at,omitempty"`
	LastSuccessAt      time.Time                      `json:"last_success_at,omitempty"`
	LastError          string                         `json:"last_error,omitempty"`
}

type StateStore interface {
	Set(context.Context, State) error
	Get(context.Context) (State, error)
}

type persistentStateStore struct {
	store pconfig.Verification
}

func NewPersistentStateStore(store pconfig.Verification) (StateStore, error) {
	if store == nil {
		return nil, errMissingState
	}

	return persistentStateStore{store: store}, nil
}

func (s persistentStateStore) Set(ctx context.Context, state State) error {
	state.Version = StateVersion
	if state.HashDomain == "" {
		state.HashDomain = HashDomainSnapshotV1Full
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return s.store.SetSnapshotPosterState(ctx, data)
}

func (s persistentStateStore) Get(ctx context.Context) (State, error) {
	data, err := s.store.GetSnapshotPosterState(ctx)
	if errors.Is(err, pconfig.ErrNotExists) {
		return State{}, ErrStateNotFound
	}
	if err != nil {
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}

	return state, nil
}
