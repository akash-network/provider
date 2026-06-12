package poster

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	verificationv1 "pkg.akt.dev/go/node/verification/v1"
	"pkg.akt.dev/go/testutil"

	aepinventory "github.com/akash-network/provider/verification/inventory"
)

const testProviderAddress = "akash1provider"

type testPayloadSource struct {
	payload []byte
	err     error
	req     aepinventory.SnapshotRequest
	called  bool
}

func (s *testPayloadSource) Payload(_ context.Context, req aepinventory.SnapshotRequest) ([]byte, error) {
	s.called = true
	s.req = req

	return append([]byte(nil), s.payload...), s.err
}

type testSigner struct {
	address sdk.AccAddress
	err     error
}

func (s testSigner) Address() sdk.AccAddress {
	return s.address
}

func (s testSigner) Sign(_ context.Context, _ []byte) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}

	return []byte("signature"), nil
}

func (s testSigner) Broadcast(context.Context, ...sdk.Msg) (*sdk.TxResponse, error) {
	return nil, errors.New("unused")
}

type testSnapshotter struct {
	snapshot *aepinventory.Snapshot
	err      error
	block    chan struct{}
	entered  chan struct{}
	called   int
}

func (s *testSnapshotter) Build(context.Context, aepinventory.SnapshotRequest) (*aepinventory.Snapshot, error) {
	s.called++
	if s.entered != nil {
		close(s.entered)
		s.entered = nil
	}
	if s.block != nil {
		<-s.block
	}

	return s.snapshot, s.err
}

type testQueryClient struct {
	record        *verificationv1.ProviderSnapshotRecord
	params        verificationv1.Params
	provider      string
	providerCalls int
	paramsCalls   int
	providerErr   error
	paramsErr     error
}

func (q *testQueryClient) ProviderSnapshot(_ context.Context, provider string) (*verificationv1.ProviderSnapshotRecord, error) {
	q.providerCalls++
	q.provider = provider

	if q.providerErr != nil {
		return nil, q.providerErr
	}

	return q.record, nil
}

func (q *testQueryClient) Params(context.Context) (verificationv1.Params, error) {
	q.paramsCalls++

	if q.paramsErr != nil {
		return verificationv1.Params{}, q.paramsErr
	}

	return q.params, nil
}

type testBroadcaster struct {
	msgs      []sdk.Msg
	err       error
	calls     int
	broadcast func(context.Context, ...sdk.Msg) (*sdk.TxResponse, error)
}

func (b *testBroadcaster) Broadcast(ctx context.Context, msgs ...sdk.Msg) (*sdk.TxResponse, error) {
	b.calls++
	b.msgs = append([]sdk.Msg(nil), msgs...)

	if b.broadcast != nil {
		return b.broadcast(ctx, msgs...)
	}

	if b.err != nil {
		return nil, b.err
	}

	return &sdk.TxResponse{TxHash: "snapshot-tx"}, nil
}

type testStateStore struct {
	state State
	err   error
	calls int
}

func (s *testStateStore) Set(_ context.Context, state State) error {
	s.calls++
	if s.err != nil {
		return s.err
	}

	s.state = state

	return nil
}

func (s *testStateStore) Get(context.Context) (State, error) {
	return s.state, s.err
}

func validSnapshot(t *testing.T, hash []byte, timestamp time.Time) *aepinventory.Snapshot {
	t.Helper()

	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      testProviderAddress,
		ChainID:       "akashnet-2",
		Timestamp:     timestamp,
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalGPUs:       2,
			TotalVCPUs:      16,
			TotalMemoryMB:   64 * 1024,
			TotalStorageMB:  1024 * 1024,
			ActiveLeases:    5,
			SoftwareVersion: "v1.2.3",
		},
	}
	payloadBytes, err := aepinventory.MarshalDeterministic(&payload)
	require.NoError(t, err)

	if hash == nil {
		hash = aepinventory.HashPayload(payloadBytes)
	}

	return &aepinventory.Snapshot{
		Payload:  payloadBytes,
		Provider: testProviderAddress,
		Hash:     append([]byte(nil), hash...),
	}
}

func TestBuildMsgFromSignedInventorySnapshot(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	softwareIdentity := testInventorySoftwareIdentity()
	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      testProviderAddress,
		ChainID:       "akashnet-2",
		Timestamp:     now,
		ResourceSummary: inventoryv1.SnapshotResourceSummary{
			TotalGPUs:         4,
			TotalVCPUs:        32,
			TotalMemoryMB:     128 * 1024,
			TotalStorageMB:    2 * 1024 * 1024,
			ActiveLeases:      11,
			SoftwareVersion:   "v1.2.3",
			SoftwareSignature: []byte("release-signature"),
			SoftwareIdentity:  softwareIdentity,
		},
	}
	payloadBytes, err := aepinventory.MarshalDeterministic(&payload)
	require.NoError(t, err)

	source := &testPayloadSource{payload: payloadBytes}
	signer := testSigner{address: testutil.AccAddress(t)}
	builder, err := aepinventory.NewBuilder(source, signer)
	require.NoError(t, err)

	prepared, err := BuildMsg(context.Background(), builder)
	require.NoError(t, err)

	require.True(t, source.called)
	require.Empty(t, source.req.Nonce)
	require.Equal(t, payload, prepared.Payload)
	require.Equal(t, signer.address.String(), prepared.Msg.Provider)
	require.Equal(t, aepinventory.HashPayload(payloadBytes), prepared.Msg.SnapshotHash)
	require.Equal(t, now, prepared.Msg.SnapshotTimestamp)
	require.Equal(t, verificationv1.ResourceSummary{
		TotalGPUs:         4,
		TotalVCPUs:        32,
		TotalMemoryMB:     128 * 1024,
		TotalStorageMB:    2 * 1024 * 1024,
		ActiveLeases:      11,
		SoftwareVersion:   "v1.2.3",
		SoftwareSignature: []byte("release-signature"),
		SoftwareIdentity:  testVerificationSoftwareIdentity(),
	}, prepared.Msg.ResourceSummary)
	require.NotSame(t, softwareIdentity, prepared.Msg.ResourceSummary.SoftwareIdentity)
}

func testInventorySoftwareIdentity() *inventoryv1.SoftwareIdentity {
	return &inventoryv1.SoftwareIdentity{
		Version:         "v1.2.3",
		ArtifactRef:     "ghcr.io/akash-network/provider:v1.2.3",
		DigestAlgorithm: "sha3-256",
		Digest:          bytes.Repeat([]byte{2}, 32),
		SignatureType:   "cosign_keyful",
		Signature:       []byte("release-signature"),
		SignatureRef:    "ghcr.io/akash-network/provider@sha256:signature",
		PublicKeyRef:    "github.com/akash-network/releases/provider.pub",
	}
}

func testVerificationSoftwareIdentity() *verificationv1.SoftwareIdentity {
	return &verificationv1.SoftwareIdentity{
		Version:         "v1.2.3",
		ArtifactRef:     "ghcr.io/akash-network/provider:v1.2.3",
		DigestAlgorithm: "sha3-256",
		Digest:          bytes.Repeat([]byte{2}, 32),
		SignatureType:   "cosign_keyful",
		Signature:       []byte("release-signature"),
		SignatureRef:    "ghcr.io/akash-network/provider@sha256:signature",
		PublicKeyRef:    "github.com/akash-network/releases/provider.pub",
	}
}

func TestBuildMsgValidatesSnapshotterAndSnapshot(t *testing.T) {
	tests := []struct {
		name        string
		snapshotter Snapshotter
		wantErr     error
	}{
		{
			name:    "missing snapshotter",
			wantErr: errMissingSnapshotter,
		},
		{
			name:        "nil snapshot",
			snapshotter: &testSnapshotter{},
			wantErr:     errMissingSnapshot,
		},
		{
			name: "missing payload",
			snapshotter: &testSnapshotter{snapshot: &aepinventory.Snapshot{
				Provider: testProviderAddress,
				Hash:     bytes.Repeat([]byte{1}, 32),
			}},
			wantErr: errMissingSnapshotPayload,
		},
		{
			name: "missing provider",
			snapshotter: &testSnapshotter{snapshot: &aepinventory.Snapshot{
				Payload: []byte("payload"),
				Hash:    bytes.Repeat([]byte{1}, 32),
			}},
			wantErr: errMissingSnapshotProvider,
		},
		{
			name: "missing hash",
			snapshotter: &testSnapshotter{snapshot: &aepinventory.Snapshot{
				Payload:  []byte("payload"),
				Provider: testProviderAddress,
			}},
			wantErr: errMissingSnapshotHash,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := BuildMsg(context.Background(), test.snapshotter)
			require.ErrorIs(t, err, test.wantErr)
			require.Nil(t, prepared)
		})
	}
}

func TestBuildMsgRejectsInvalidSnapshotPayload(t *testing.T) {
	prepared, err := BuildMsg(context.Background(), &testSnapshotter{snapshot: &aepinventory.Snapshot{
		Payload:  []byte("not proto"),
		Provider: testProviderAddress,
		Hash:     bytes.Repeat([]byte{1}, 32),
	}})
	require.Error(t, err)
	require.Nil(t, prepared)
}

func TestBuildMsgRejectsMissingSnapshotTimestamp(t *testing.T) {
	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      testProviderAddress,
		ChainID:       "akashnet-2",
	}
	payloadBytes, err := aepinventory.MarshalDeterministic(&payload)
	require.NoError(t, err)

	prepared, err := BuildMsg(context.Background(), &testSnapshotter{snapshot: &aepinventory.Snapshot{
		Payload:  payloadBytes,
		Provider: testProviderAddress,
		Hash:     aepinventory.HashPayload(payloadBytes),
	}})
	require.ErrorIs(t, err, errMissingSnapshotTimestamp)
	require.Nil(t, prepared)
}

func TestBuildMsgRejectsSnapshotNonce(t *testing.T) {
	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      testProviderAddress,
		ChainID:       "akashnet-2",
		Nonce:         bytes.Repeat([]byte{1}, aepinventory.NonceSize),
		Timestamp:     time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC),
	}
	payloadBytes, err := aepinventory.MarshalDeterministic(&payload)
	require.NoError(t, err)

	prepared, err := BuildMsg(context.Background(), &testSnapshotter{snapshot: &aepinventory.Snapshot{
		Payload:  payloadBytes,
		Provider: testProviderAddress,
		Hash:     aepinventory.HashPayload(payloadBytes),
	}})
	require.ErrorIs(t, err, errUnexpectedSnapshotNonce)
	require.Nil(t, prepared)
}

func TestShouldPost(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	hash := bytes.Repeat([]byte{1}, 32)

	tests := []struct {
		name   string
		input  DecisionInput
		expect Decision
	}{
		{
			name: "missing record",
			input: DecisionInput{
				SnapshotHash: hash,
				Now:          now,
			},
			expect: Decision{Post: true, Reason: DecisionReasonMissingRecord},
		},
		{
			name: "suspended",
			input: DecisionInput{
				SnapshotHash: hash,
				Record: &verificationv1.ProviderSnapshotRecord{
					Provider:           testProviderAddress,
					SnapshotHash:       hash,
					ComplianceDeadline: now.Add(time.Hour),
					Suspended:          true,
				},
				Now: now,
			},
			expect: Decision{Post: true, Reason: DecisionReasonSuspended},
		},
		{
			name: "hash changed",
			input: DecisionInput{
				SnapshotHash: hash,
				Record: &verificationv1.ProviderSnapshotRecord{
					Provider:           testProviderAddress,
					SnapshotHash:       bytes.Repeat([]byte{2}, 32),
					ComplianceDeadline: now.Add(time.Hour),
				},
				Now: now,
			},
			expect: Decision{Post: true, Reason: DecisionReasonHashChanged},
		},
		{
			name: "missing deadline",
			input: DecisionInput{
				SnapshotHash: hash,
				Record: &verificationv1.ProviderSnapshotRecord{
					Provider:     testProviderAddress,
					SnapshotHash: hash,
				},
				Now: now,
			},
			expect: Decision{Post: true, Reason: DecisionReasonMissingDeadline},
		},
		{
			name: "deadline due inside lead time",
			input: DecisionInput{
				SnapshotHash: hash,
				Record: &verificationv1.ProviderSnapshotRecord{
					Provider:           testProviderAddress,
					SnapshotHash:       hash,
					ComplianceDeadline: now.Add(10 * time.Minute),
				},
				Now:                now,
				PostBeforeDeadline: 15 * time.Minute,
			},
			expect: Decision{Post: true, Reason: DecisionReasonDeadlineDue},
		},
		{
			name: "deadline unchanged",
			input: DecisionInput{
				SnapshotHash: hash,
				Record: &verificationv1.ProviderSnapshotRecord{
					Provider:           testProviderAddress,
					SnapshotHash:       hash,
					ComplianceDeadline: now.Add(time.Hour),
				},
				Now:                now,
				PostBeforeDeadline: 15 * time.Minute,
			},
			expect: Decision{Post: false, Reason: DecisionReasonDeadlineUnchanged},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expect, ShouldPost(test.input))
		})
	}
}

func TestRunnerRunOncePostsWhenProviderSnapshotMissing(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	provider := testProviderAddress
	hash := bytes.Repeat([]byte{1}, 32)

	query := &testQueryClient{
		params: verificationv1.Params{
			SnapshotHashInterval:     time.Hour,
			VerificationModuleActive: false,
		},
		providerErr: ErrProviderSnapshotNotFound,
	}
	broadcaster := &testBroadcaster{}
	state := &testStateStore{}
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, hash, now)},
		Query:       query,
		Broadcaster: broadcaster,
		State:       state,
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	result, err := runner.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, Decision{Post: true, Reason: DecisionReasonMissingRecord}, result.Decision)
	require.Equal(t, verificationv1.Params{
		SnapshotHashInterval:     time.Hour,
		VerificationModuleActive: false,
	}, result.Params)
	require.Equal(t, provider, query.provider)
	require.Equal(t, 1, query.paramsCalls)
	require.Equal(t, 1, query.providerCalls)
	require.Equal(t, 1, broadcaster.calls)
	require.Len(t, broadcaster.msgs, 1)

	msg, ok := broadcaster.msgs[0].(*verificationv1.MsgPostSnapshotHash)
	require.True(t, ok)
	require.Equal(t, provider, msg.Provider)
	require.Equal(t, hash, msg.SnapshotHash)
	require.Equal(t, "snapshot-tx", result.Response.TxHash)
	require.Equal(t, 1, state.calls)
	require.Equal(t, HashDomainSnapshotV1Full, state.state.HashDomain)
	require.Equal(t, provider, state.state.Provider)
	require.Equal(t, hash, state.state.SnapshotHash)
	require.Equal(t, "snapshot-tx", state.state.LastTxHash)
	require.Equal(t, now, state.state.LastSuccessAt)
}

func TestRunnerRunOnceSkipsWhenSnapshotHashAndDeadlineUnchanged(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	provider := testProviderAddress
	hash := bytes.Repeat([]byte{1}, 32)

	query := &testQueryClient{
		record: &verificationv1.ProviderSnapshotRecord{
			Provider:           provider,
			SnapshotHash:       hash,
			ComplianceDeadline: now.Add(2 * time.Hour),
		},
		params: verificationv1.Params{
			SnapshotHashInterval:     time.Hour,
			VerificationModuleActive: true,
		},
	}
	broadcaster := &testBroadcaster{}
	state := &testStateStore{}
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, hash, now)},
		Query:       query,
		Broadcaster: broadcaster,
		State:       state,
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	result, err := runner.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, Decision{Post: false, Reason: DecisionReasonDeadlineUnchanged}, result.Decision)
	require.Equal(t, query.record, result.Record)
	require.Equal(t, 0, broadcaster.calls)
	require.Nil(t, result.Response)
	require.Equal(t, 1, state.calls)
	require.Equal(t, string(DecisionReasonDeadlineUnchanged), state.state.LastDecision)
	require.True(t, state.state.LastSuccessAt.IsZero())
}

func TestRunnerRunOncePostsBeforeDeadlineUsingParamsInterval(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	provider := testProviderAddress
	hash := bytes.Repeat([]byte{1}, 32)

	query := &testQueryClient{
		record: &verificationv1.ProviderSnapshotRecord{
			Provider:           provider,
			SnapshotHash:       hash,
			ComplianceDeadline: now.Add(5 * time.Minute),
		},
		params: verificationv1.Params{
			SnapshotHashInterval:     time.Hour,
			VerificationModuleActive: true,
		},
	}
	broadcaster := &testBroadcaster{}
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, hash, now)},
		Query:       query,
		Broadcaster: broadcaster,
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	result, err := runner.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, Decision{Post: true, Reason: DecisionReasonDeadlineDue}, result.Decision)
	require.Equal(t, 1, broadcaster.calls)
}

func TestRunnerRunOnceReturnsBroadcastError(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	broadcastErr := errors.New("broadcast failed")
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, bytes.Repeat([]byte{1}, 32), now)},
		Query: &testQueryClient{
			params: verificationv1.Params{VerificationModuleActive: true},
		},
		Broadcaster: &testBroadcaster{err: broadcastErr},
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	result, err := runner.RunOnce(context.Background())
	require.ErrorIs(t, err, broadcastErr)
	require.Nil(t, result)
}

func TestNewRunnerValidatesDependencies(t *testing.T) {
	tests := []struct {
		name string
		cfg  RunnerConfig
		err  error
	}{
		{
			name: "missing snapshotter",
			err:  errMissingSnapshotter,
		},
		{
			name: "missing query",
			cfg: RunnerConfig{
				Snapshotter: &testSnapshotter{snapshot: &aepinventory.Snapshot{}},
			},
			err: errMissingQueryClient,
		},
		{
			name: "missing broadcaster",
			cfg: RunnerConfig{
				Snapshotter: &testSnapshotter{snapshot: &aepinventory.Snapshot{}},
				Query:       &testQueryClient{},
			},
			err: errMissingBroadcaster,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner, err := NewRunner(test.cfg)
			require.ErrorIs(t, err, test.err)
			require.Nil(t, runner)
		})
	}
}

func TestRunnerRunOncePostsWhenVerificationModuleInactive(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	snapshotter := &testSnapshotter{snapshot: validSnapshot(t, bytes.Repeat([]byte{1}, 32), now)}
	query := &testQueryClient{
		params: verificationv1.Params{
			SnapshotHashInterval:     time.Hour,
			VerificationModuleActive: false,
		},
		providerErr: ErrProviderSnapshotNotFound,
	}
	broadcaster := &testBroadcaster{}
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: snapshotter,
		Query:       query,
		Broadcaster: broadcaster,
	})
	require.NoError(t, err)

	result, err := runner.RunOnce(context.Background())
	require.NoError(t, err)

	require.Equal(t, Decision{Post: true, Reason: DecisionReasonMissingRecord}, result.Decision)
	require.NotNil(t, result.Prepared)
	require.Equal(t, 1, query.paramsCalls)
	require.Equal(t, 1, query.providerCalls)
	require.Equal(t, 1, snapshotter.called)
	require.Equal(t, 1, broadcaster.calls)
}

func TestRunnerRunOnceSingleFlight(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{block: block, entered: entered},
		Query: &testQueryClient{
			params: verificationv1.Params{VerificationModuleActive: true},
		},
		Broadcaster: &testBroadcaster{},
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunOnce(context.Background())
		done <- err
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first RunOnce to enter Build")
	}

	_, err = runner.RunOnce(context.Background())
	require.ErrorIs(t, err, errPosterAlreadyRunning)

	close(block)
	require.ErrorIs(t, <-done, errMissingSnapshot)
}

func TestRunnerRunRetriesBoundedFailures(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	broadcastErr := errors.New("broadcast failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broadcaster := &testBroadcaster{}
	broadcaster.broadcast = func(context.Context, ...sdk.Msg) (*sdk.TxResponse, error) {
		if broadcaster.calls == 1 {
			return nil, broadcastErr
		}

		cancel()
		return &sdk.TxResponse{TxHash: "snapshot-tx"}, nil
	}

	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, bytes.Repeat([]byte{1}, 32), now)},
		Query: &testQueryClient{
			params: verificationv1.Params{VerificationModuleActive: true},
		},
		Broadcaster: broadcaster,
		Interval:    time.Hour,
		RetryDelay:  time.Millisecond,
		MaxRetries:  1,
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	err = runner.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, broadcaster.calls)
}

func TestRunnerRunContinuesAfterBoundedFailures(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broadcaster := &testBroadcaster{}
	broadcaster.broadcast = func(context.Context, ...sdk.Msg) (*sdk.TxResponse, error) {
		if broadcaster.calls == 2 {
			cancel()
		}

		return nil, errors.New("broadcast failed")
	}

	runner, err := NewRunner(RunnerConfig{
		Snapshotter: &testSnapshotter{snapshot: validSnapshot(t, bytes.Repeat([]byte{1}, 32), now)},
		Query: &testQueryClient{
			params: verificationv1.Params{VerificationModuleActive: true},
		},
		Broadcaster: broadcaster,
		Interval:    time.Hour,
		RetryDelay:  time.Millisecond,
		MaxRetries:  1,
		Now: func() time.Time {
			return now
		},
	})
	require.NoError(t, err)

	err = runner.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, broadcaster.calls)
}
