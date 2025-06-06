package grpc

import (
	"crypto/x509"
	"fmt"
	"net"
	"time"

	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/akash-network/akash-api/go/grpc/gogoreflection"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/provider"
	gwutils "github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

type ContextKey string

const (
	ContextKeyClaims = ContextKey("owner")
)

type grpcProviderV1 struct {
	ctx    context.Context
	client provider.StatusClient
	config *provider.Config
}

var _ providerv1.ProviderRPCServer = (*grpcProviderV1)(nil)

func ContextWithClaims(ctx context.Context, claims *ajwt.Claims) context.Context {
	return context.WithValue(ctx, ContextKeyClaims, claims)
}

func ClaimsFromCtx(ctx context.Context) *ajwt.Claims {
	val := ctx.Value(ContextKeyClaims)
	if val == nil {
		return &ajwt.Claims{}
	}

	return val.(*ajwt.Claims)
}

func NewServer(ctx context.Context, endpoint string, cquery gwutils.CertGetter, client provider.StatusClient, config *provider.Config) error {
	tlsCfg, err := gwutils.NewServerTLSConfig(ctx, cquery, endpoint)
	if err != nil {
		return err
	}

	group, err := fromctx.ErrGroupFromCtx(ctx)
	if err != nil {
		return err
	}

	log := fromctx.LogcFromCtx(ctx)

	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)), grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             30 * time.Second,
		PermitWithoutStream: false,
	}), grpc.ChainUnaryInterceptor(authInterceptor()))

	pRPC := &grpcProviderV1{
		ctx:    ctx,
		client: client,
		config: config,
	}

	providerv1.RegisterProviderRPCServer(grpcSrv, pRPC)
	gogoreflection.Register(grpcSrv)

	group.Go(func() error {
		grpcLis, err := net.Listen("tcp", endpoint)
		if err != nil {
			return err
		}

		log.Info(fmt.Sprintf("grpc listening on \"%s\"", endpoint))

		return grpcSrv.Serve(grpcLis)
	})

	group.Go(func() error {
		<-ctx.Done()

		grpcSrv.GracefulStop()

		return ctx.Err()
	})

	return nil
}

func authInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		var tokString string
		var peerCerts []*x509.Certificate

		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				peerCerts = mtls.State.PeerCertificates
			}
		}

		if md, ok := metadata.FromIncomingContext(ctx); ok {
			tokens := md["authorization"]
			if len(tokens) == 1 {
				tokString = tokens[1]
			}
		}

		claims, err := gwutils.AuthProcess(ctx, peerCerts, tokString)
		if err != nil {
			return nil, err
		}

		ctx = ContextWithClaims(ctx, claims)

		return handler(ctx, req)
	}
}

func (gm *grpcProviderV1) GetStatus(ctx context.Context, _ *emptypb.Empty) (*providerv1.Status, error) {
	return gm.client.StatusV1(ctx)
}

func (gm *grpcProviderV1) StreamStatus(_ *emptypb.Empty, stream providerv1.ProviderRPC_StreamStatusServer) error {
	bus, err := fromctx.PubSubFromCtx(gm.ctx)
	if err != nil {
		return err
	}

	events := bus.Sub(ptypes.PubSubTopicProviderStatus)

	for {
		select {
		case <-gm.ctx.Done():
			return gm.ctx.Err()
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
