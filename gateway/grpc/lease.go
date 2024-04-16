package grpc

import (
	"context"

	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"

	"github.com/akash-network/provider"
)

type leaseV1 struct {
	c   provider.Client
	ctx context.Context
}

func (l *leaseV1) SendManifest(context.Context, *leasev1.SendManifestRequest) (*leasev1.SendManifestResponse, error) {
	panic("unimplemented")
}

func (l *leaseV1) ServiceLogs(context.Context, *leasev1.ServiceLogsRequest) (*leasev1.ServiceLogsResponse, error) {
	panic("unimplemented")
}

func (l *leaseV1) ServiceStatus(context.Context, *leasev1.ServiceStatusRequest) (*leasev1.ServiceStatusResponse, error) {
	panic("unimplemented")
}

func (l *leaseV1) StreamServiceLogs(*leasev1.ServiceLogsRequest, leasev1.LeaseRPC_StreamServiceLogsServer) error {
	panic("unimplemented")
}

func (l *leaseV1) StreamServiceStatus(*leasev1.ServiceStatusRequest, leasev1.LeaseRPC_StreamServiceStatusServer) error {
	panic("unimplemented")
}
