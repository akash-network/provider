package grpc

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/verification/inventory"
)

type testInventorySnapshotter struct {
	snapshot *inventory.Snapshot
	err      error
	req      inventory.SnapshotRequest
	called   bool
}

func (s *testInventorySnapshotter) Build(_ context.Context, req inventory.SnapshotRequest) (*inventory.Snapshot, error) {
	s.called = true
	s.req = req

	return s.snapshot, s.err
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

func TestGetInventorySnapshotReturnsUnavailableWithoutSnapshotter(t *testing.T) {
	server := &grpcInventoryV1{}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{})
	require.Nil(t, resp)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestGetInventorySnapshotReturnsSnapshotterError(t *testing.T) {
	expected := errors.New("snapshot failed")
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{err: expected},
	}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{})
	require.Nil(t, resp)
	require.ErrorIs(t, err, expected)
}

func TestGetInventorySnapshotRejectsNilSnapshot(t *testing.T) {
	server := &grpcInventoryV1{
		snapshotter: &testInventorySnapshotter{},
	}

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{})
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

	resp, err := server.GetInventorySnapshot(context.Background(), &inventoryv1.GetInventorySnapshotRequest{})
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "missing inventory snapshot payload")
}
