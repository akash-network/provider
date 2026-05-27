package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/verification/inventory"
)

// InventorySnapshotter is the provider-side builder behind the AEP-86
// inventory snapshot RPC.
type InventorySnapshotter interface {
	Build(context.Context, inventory.SnapshotRequest) (*inventory.Snapshot, error)
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

	snapshot, err := gm.snapshotter.Build(ctx, inventory.SnapshotRequest{
		Nonce: req.GetNonce(),
	})
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, status.Error(codes.Internal, "inventory snapshot builder returned nil snapshot")
	}

	return &inventoryv1.GetInventorySnapshotResponse{
		SnapshotPayload: snapshot.Payload,
		Signature:       snapshot.Signature,
		Provider:        snapshot.Provider,
	}, nil
}
