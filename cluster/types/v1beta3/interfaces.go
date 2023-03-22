package v1beta2

import (
	"context"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
)

//go:generate mockery --name HostnameServiceClient --output ../../mocks
type HostnameServiceClient interface {
	ReserveHostnames(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) ([]string, error)
	ReleaseHostnames(leaseID mtypes.LeaseID) error
	CanReserveHostnames(hostnames []string, ownerAddr sdktypes.Address) error
	PrepareHostnamesForTransfer(ctx context.Context, hostnames []string, leaseID mtypes.LeaseID) error
}
