package grpc

import (
	"context"
	"crypto/sha256"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/verification/inventory"
)

// InventorySnapshotter is the provider-side builder behind the AEP-86
// inventory snapshot RPC.
type InventorySnapshotter interface {
	Build(context.Context, inventory.SnapshotRequest) (*inventory.Snapshot, error)
	LatestCommitted(context.Context) (inventory.SnapshotRecord, bool, error)
	Committed(context.Context, []byte) (inventory.SnapshotRecord, bool, error)
}

type grpcInventoryV1 struct {
	snapshotter InventorySnapshotter
}

var _ inventoryv1.InventoryServiceServer = (*grpcInventoryV1)(nil)

func (gm *grpcInventoryV1) GetInventorySnapshot(ctx context.Context, req *inventoryv1.GetInventorySnapshotRequest) (*inventoryv1.GetInventorySnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if gm.snapshotter == nil {
		return nil, status.Error(codes.Unavailable, "inventory snapshot service unavailable")
	}

	if err := inventory.ValidateNonce(req.GetNonce()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(req.GetNonce()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing inventory snapshot nonce")
	}

	snapshot, err := gm.snapshotter.Build(ctx, inventory.SnapshotRequest{
		Nonce: req.GetNonce(),
	})
	if err != nil {
		return nil, err
	}
	if err := inventory.ValidateSnapshot(snapshot); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &inventoryv1.GetInventorySnapshotResponse{
		SnapshotPayload: snapshot.Payload,
		Signature:       snapshot.Signature,
		Provider:        snapshot.Provider,
	}, nil
}

func (gm *grpcInventoryV1) GetCommittedInventorySnapshot(ctx context.Context, req *inventoryv1.GetCommittedInventorySnapshotRequest) (*inventoryv1.GetCommittedInventorySnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}

	if gm.snapshotter == nil {
		return nil, status.Error(codes.Unavailable, "inventory snapshot service unavailable")
	}

	var (
		record inventory.SnapshotRecord
		ok     bool
		err    error
	)
	if len(req.GetSnapshotHash()) == 0 {
		record, ok, err = gm.snapshotter.LatestCommitted(ctx)
	} else {
		if len(req.GetSnapshotHash()) != sha256.Size {
			return nil, status.Errorf(codes.InvalidArgument, "invalid committed inventory snapshot hash length: expected %d bytes, got %d", sha256.Size, len(req.GetSnapshotHash()))
		}
		record, ok, err = gm.snapshotter.Committed(ctx, req.GetSnapshotHash())
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "committed inventory snapshot not found")
	}

	snapshot := record.Snapshot
	if err := inventory.ValidateSnapshot(&snapshot); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &inventoryv1.GetCommittedInventorySnapshotResponse{
		SnapshotPayload: snapshot.Payload,
		Signature:       snapshot.Signature,
		Provider:        snapshot.Provider,
		SnapshotHash:    snapshot.Hash,
		PostedAt:        record.CreatedAt,
	}, nil
}
