package grpc

import (
	"fmt"
	"runtime/debug"

	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
	"github.com/akash-network/provider/version"
)

func (s *server) GetStatus(ctx context.Context, _ *emptypb.Empty) (*providerv1.Status, error) {
	return s.pc.StatusV1(ctx)
}

func (s *server) StreamStatus(_ *emptypb.Empty, stream providerv1.ProviderRPC_StreamStatusServer) error {
	bus, err := fromctx.PubSubFromCtx(s.ctx)
	if err != nil {
		return err
	}

	events := bus.Sub(ptypes.PubSubTopicProviderStatus)

	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		case evt := <-events:
			val := evt.(providerv1.Status)
			if err := stream.Send(&val); err != nil {
				return err
			}
		}
	}
}

func (s *server) GetVersion(ctx context.Context, _ *emptypb.Empty) (*providerv1.GetVersionResponse, error) {
	v := version.NewInfo()

	k, err := s.cc.KubeVersion()
	if err != nil {
		return nil, fmt.Errorf("kube version: %w", err)
	}

	bd := make([]*providerv1.BuildDep, 0, len(v.BuildDeps))
	for _, b := range v.BuildDeps {
		bd = append(bd, toProviderv1BuildDep(b.Module))
	}

	return &providerv1.GetVersionResponse{
		Akash: &providerv1.AkashInfo{
			Name:      v.Name,
			AppName:   v.AppName,
			Version:   v.Version,
			GitCommit: v.GitCommit,
			BuildTags: v.BuildTags,
			GoVersion: v.GoVersion,
			BuildDeps: bd,
		},
		Kube: &providerv1.KubeInfo{
			Major:        k.Major,
			Minor:        k.Minor,
			GitVersion:   k.GitCommit,
			GitTreeState: k.GitTreeState,
			BuildDate:    k.BuildDate,
			GoVersion:    k.GoVersion,
			Compiler:     k.Compiler,
			Platform:     k.Platform,
		},
	}, nil
}

func toProviderv1BuildDep(m *debug.Module) *providerv1.BuildDep {
	if m == nil {
		return nil
	}

	return &providerv1.BuildDep{
		Path:    m.Path,
		Version: m.Version,
		Sum:     m.Sum,
		Replace: toProviderv1BuildDep(m.Replace),
	}
}

func (s *server) Validate(ctx context.Context, r *providerv1.ValidateRequest) (*providerv1.ValidateResponse, error) {
	v, err := s.pc.Validate(ctx, OwnerFromCtx(ctx), r.GetGroup())
	if err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}

	return &providerv1.ValidateResponse{
		MinBidPrice: v.MinBidPrice,
	}, nil
}

func (s *server) WIBOY(ctx context.Context, r *providerv1.ValidateRequest) (*providerv1.ValidateResponse, error) {
	return s.Validate(ctx, r)
}
