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
}

func (s testSnapshotter) Build(context.Context, aepinventory.SnapshotRequest) (*aepinventory.Snapshot, error) {
	return s.snapshot, s.err
}

func TestBuildMsgFromSignedInventorySnapshot(t *testing.T) {
	now := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC)
	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
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
	}, prepared.Msg.ResourceSummary)
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
			snapshotter: testSnapshotter{},
			wantErr:     errMissingSnapshot,
		},
		{
			name: "missing payload",
			snapshotter: testSnapshotter{snapshot: &aepinventory.Snapshot{
				Provider: "akash1provider",
				Hash:     bytes.Repeat([]byte{1}, 32),
			}},
			wantErr: errMissingSnapshotPayload,
		},
		{
			name: "missing provider",
			snapshotter: testSnapshotter{snapshot: &aepinventory.Snapshot{
				Payload: []byte("payload"),
				Hash:    bytes.Repeat([]byte{1}, 32),
			}},
			wantErr: errMissingSnapshotProvider,
		},
		{
			name: "missing hash",
			snapshotter: testSnapshotter{snapshot: &aepinventory.Snapshot{
				Payload:  []byte("payload"),
				Provider: "akash1provider",
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
	prepared, err := BuildMsg(context.Background(), testSnapshotter{snapshot: &aepinventory.Snapshot{
		Payload:  []byte("not proto"),
		Provider: "akash1provider",
		Hash:     bytes.Repeat([]byte{1}, 32),
	}})
	require.Error(t, err)
	require.Nil(t, prepared)
}

func TestBuildMsgRejectsMissingSnapshotTimestamp(t *testing.T) {
	payload := inventoryv1.SnapshotPayload{
		SchemaVersion: aepinventory.SnapshotPayloadSchemaVersion,
		Provider:      "akash1provider",
		ChainID:       "akashnet-2",
	}
	payloadBytes, err := aepinventory.MarshalDeterministic(&payload)
	require.NoError(t, err)

	prepared, err := BuildMsg(context.Background(), testSnapshotter{snapshot: &aepinventory.Snapshot{
		Payload:  payloadBytes,
		Provider: "akash1provider",
		Hash:     aepinventory.HashPayload(payloadBytes),
	}})
	require.ErrorIs(t, err, errMissingSnapshotTimestamp)
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
					Provider:           "akash1provider",
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
					Provider:           "akash1provider",
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
					Provider:     "akash1provider",
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
					Provider:           "akash1provider",
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
					Provider:           "akash1provider",
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
