package grpc

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/verification/inventory"
)

type testInventorySnapshotter struct {
	snapshot        *inventory.Snapshot
	err             error
	req             inventory.SnapshotRequest
	called          bool
	latestRecord    inventory.SnapshotRecord
	latestOK        bool
	committedRecord inventory.SnapshotRecord
	committedOK     bool
	committedHash   []byte
}

func (s *testInventorySnapshotter) Build(_ context.Context, req inventory.SnapshotRequest) (*inventory.Snapshot, error) {
	s.called = true
	s.req = req

	return s.snapshot, s.err
}

func (s *testInventorySnapshotter) LatestCommitted(context.Context) (inventory.SnapshotRecord, bool, error) {
	return s.latestRecord, s.latestOK, s.err
}

func (s *testInventorySnapshotter) Committed(_ context.Context, hash []byte) (inventory.SnapshotRecord, bool, error) {
	s.committedHash = append([]byte(nil), hash...)
	return s.committedRecord, s.committedOK, s.err
}

func TestGetInventorySnapshotBuildsResponse(t *testing.T) {
	nonce := bytes.Repeat([]byte{1}, inventory.NonceSize)
	snapshotter := &testInventorySnapshotter{
		snapshot: &inventory.Snapshot{
			Payload:   []byte("payload"),
			Hash:      []byte("hash"),
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		},
	}
	server := &grpcInventoryV1{snapshotter: snapshotter}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: nonce,
	})
	require.NoError(t, err)

	require.True(t, snapshotter.called)
	require.Equal(t, nonce, snapshotter.req.Nonce)
	require.Equal(t, []byte("payload"), resp.SnapshotPayload)
	require.Equal(t, []byte("signature"), resp.Signature)
	require.Equal(t, "akash1provider", resp.Provider)
}

func TestGetInventorySnapshotRejectsEmptyRequest(t *testing.T) {
	server := &grpcInventoryV1{snapshotter: &testInventorySnapshotter{}}

	resp, err := server.GetInventorySnapshot(context.Background(), nil)
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetInventorySnapshotRejectsInvalidNonce(t *testing.T) {
	snapshotter := &testInventorySnapshotter{}
	server := &grpcInventoryV1{snapshotter: snapshotter}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: bytes.Repeat([]byte{1}, inventory.NonceSize-1),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.False(t, snapshotter.called)
}

func TestGetInventorySnapshotRejectsMissingNonce(t *testing.T) {
	snapshotter := &testInventorySnapshotter{}
	server := &grpcInventoryV1{snapshotter: snapshotter}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "missing inventory snapshot nonce")
	require.False(t, snapshotter.called)
}

func TestGetInventorySnapshotReturnsUnavailableWithoutSnapshotter(t *testing.T) {
	server := &grpcInventoryV1{}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: bytes.Repeat([]byte{1}, inventory.NonceSize),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestGetInventorySnapshotReturnsSnapshotterError(t *testing.T) {
	expected := errors.New("snapshot failed")
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{err: expected},
	}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: bytes.Repeat([]byte{1}, inventory.NonceSize),
	})
	require.Nil(t, resp)
	require.ErrorIs(t, err, expected)
}

func TestGetInventorySnapshotRejectsNilSnapshot(t *testing.T) {
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{},
	}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: bytes.Repeat([]byte{1}, inventory.NonceSize),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestGetInventorySnapshotRejectsInvalidSnapshot(t *testing.T) {
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{snapshot: &inventory.Snapshot{
			Hash:      []byte("hash"),
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		}},
	}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{
		Nonce: bytes.Repeat([]byte{1}, inventory.NonceSize),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "missing inventory snapshot payload")
}

func TestGetCommittedInventorySnapshotReturnsLatest(t *testing.T) {
	createdAt := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	record := inventory.SnapshotRecord{
		Snapshot: inventory.Snapshot{
			Payload:   []byte("payload"),
			Hash:      bytes.Repeat([]byte{2}, 32),
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		},
		CreatedAt: createdAt,
	}
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{
			latestRecord: record,
			latestOK:     true,
		},
	}

	resp, err := server.GetCommittedInventorySnapshot(context.Background(), &inventoryv1.GetCommittedInventorySnapshotRequest{})
	require.NoError(t, err)
	require.Equal(t, record.Snapshot.Payload, resp.GetSnapshotPayload())
	require.Equal(t, record.Snapshot.Signature, resp.GetSignature())
	require.Equal(t, record.Snapshot.Provider, resp.GetProvider())
	require.Equal(t, record.Snapshot.Hash, resp.GetSnapshotHash())
	require.Equal(t, createdAt, resp.GetPostedAt())
}

func TestGetCommittedInventorySnapshotReturnsHashLookup(t *testing.T) {
	hash := bytes.Repeat([]byte{3}, 32)
	record := inventory.SnapshotRecord{
		Snapshot: inventory.Snapshot{
			Payload:   []byte("payload"),
			Hash:      hash,
			Signature: []byte("signature"),
			Provider:  "akash1provider",
		},
		CreatedAt: time.Unix(1, 0).UTC(),
	}
	snapshotter := &testInventorySnapshotter{
		committedRecord: record,
		committedOK:     true,
	}
	server := &grpcInventoryV1{snapshotter: snapshotter}

	resp, err := server.GetCommittedInventorySnapshot(context.Background(), &inventoryv1.GetCommittedInventorySnapshotRequest{
		SnapshotHash: hash,
	})
	require.NoError(t, err)
	require.Equal(t, hash, snapshotter.committedHash)
	require.Equal(t, record.Snapshot.Payload, resp.GetSnapshotPayload())
	require.Equal(t, hash, resp.GetSnapshotHash())
}

func TestGetCommittedInventorySnapshotRejectsInvalidHash(t *testing.T) {
	snapshotter := &testInventorySnapshotter{}
	server := &grpcInventoryV1{snapshotter: snapshotter}

	resp, err := server.GetCommittedInventorySnapshot(context.Background(), &inventoryv1.GetCommittedInventorySnapshotRequest{
		SnapshotHash: bytes.Repeat([]byte{1}, 31),
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, snapshotter.committedHash)
}

func TestGetCommittedInventorySnapshotReturnsNotFound(t *testing.T) {
	server := &grpcInventoryV1{snapshotter: &testInventorySnapshotter{}}

	resp, err := server.GetCommittedInventorySnapshot(context.Background(), &inventoryv1.GetCommittedInventorySnapshotRequest{})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}
