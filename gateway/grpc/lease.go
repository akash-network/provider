package grpc

import (
	"context"
	"errors"

	manifestValidation "github.com/akash-network/akash-api/go/manifest/v2beta2"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/provider"
	pmanifest "github.com/akash-network/provider/manifest"
)

type leaseV1 struct {
	c   provider.Client
	ctx context.Context
}

func (l *leaseV1) SendManifest(ctx context.Context, r *leasev1.SendManifestRequest) (*leasev1.SendManifestResponse, error) {
	var (
		id = r.GetLeaseId().DeploymentID()
		m  = r.GetManifest()
	)

	// HACK(andrewhare): Existing manifests expected service resource endpoints
	// to be JSON serialized as [] instead of null when determining the manifest
	// version hash. This forces Go to do the right thing.
	for g := range m {
		for s := range m[g].Services {
			if len(m[g].Services[s].Resources.Endpoints) == 0 {
				m[g].Services[s].Resources.Endpoints = make(types.Endpoints, 0)
			}
		}
	}

	err := l.c.Manifest().Submit(ctx, id, m)
	if err == nil {
		return &leasev1.SendManifestResponse{}, nil
	}

	switch {
	case errors.Is(err, manifestValidation.ErrInvalidManifest):
		return nil, status.Error(codes.InvalidArgument, "invalid manifest")
	case errors.Is(err, pmanifest.ErrNoLeaseForDeployment):
		return nil, status.Error(codes.NotFound, "no lease for deployment")
	}

	return nil, status.Errorf(codes.Internal, "manifest submit: %v", err)
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
