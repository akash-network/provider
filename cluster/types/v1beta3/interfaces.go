package v1beta3

import (
	"context"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
)

//go:generate mockery --name HostnameServiceClient --output ../../mocks
type HostnameServiceClient interface {
	ReserveHostnames(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) ([]string, error)
	ReleaseHostnames(leaseID mtypes.LeaseID) error
	CanReserveHostnames(hostnames []string, ownerAddr sdktypes.Address) error
	PrepareHostnamesForTransfer(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) error
}

type MGroup interface {
	ManifestGroup() mani.Group
}
