package grpc

import (
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/provider"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/types/known/emptypb"
)

type providerV1 struct {
	ctx context.Context
	c   provider.Client
}

func (p *providerV1) GetStatus(ctx context.Context, _ *emptypb.Empty) (*providerv1.Status, error) {
	return p.c.StatusV1(ctx)
}

func (p *providerV1) StreamStatus(_ *emptypb.Empty, stream providerv1.ProviderRPC_StreamStatusServer) error {
	bus, err := fromctx.PubSubFromCtx(p.ctx)
	if err != nil {
		return err
	}

	events := bus.Sub(ptypes.PubSubTopicProviderStatus)

	for {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
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
