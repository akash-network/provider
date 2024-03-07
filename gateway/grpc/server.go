package grpc

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/akash-api/go/grpc/gogoreflection"
	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/provider"
	"github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

type ContextKey string

const (
	ContextKeyQueryClient = ContextKey("query-client")
	ContextKeyOwner       = ContextKey("owner")
)

type grpcProviderV1 struct {
	ctx    context.Context
	client provider.StatusClient
}

var _ providerv1.ProviderRPCServer = (*grpcProviderV1)(nil)

func QueryClientFromCtx(ctx context.Context) ctypes.QueryClient {
	val := ctx.Value(ContextKeyQueryClient)
	if val == nil {
		panic("context does not have pubsub set")
	}

	return val.(ctypes.QueryClient)
}

func ContextWithOwner(ctx context.Context, address sdk.Address) context.Context {
	return context.WithValue(ctx, ContextKeyOwner, address)
}

func OwnerFromCtx(ctx context.Context) sdk.Address {
	val := ctx.Value(ContextKeyOwner)
	if val == nil {
		return sdk.AccAddress{}
	}

	return val.(sdk.Address)
}

func Serve(ctx context.Context, endpoint string, certs []tls.Certificate, client provider.StatusClient, cquery ctypes.QueryClient) error {
	group, err := fromctx.ErrGroupFromCtx(ctx)
	if err != nil {
		return err
	}

	var (
		grpcSrv = newServer(ctx, certs, client, cquery)
		log     = fromctx.LogcFromCtx(ctx)
	)

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

func newServer(ctx context.Context, certs []tls.Certificate, client provider.StatusClient, cquery ctypes.QueryClient) *grpc.Server {
	// InsecureSkipVerify is set to true due to inability to use normal TLS verification
	// certificate validation and authentication performed later in mtlsHandler
	tlsConfig := &tls.Config{
		Certificates:       certs,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true, // nolint: gosec
		MinVersion:         tls.VersionTLS13,
	}

	grpcSrv := grpc.NewServer(grpc.Creds(
		credentials.NewTLS(tlsConfig)),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: false,
		}),
		grpc.ChainUnaryInterceptor(mtlsInterceptor(cquery)))

	pRPC := &grpcProviderV1{
		ctx:    ctx,
		client: client,
	}

	providerv1.RegisterProviderRPCServer(grpcSrv, pRPC)
	gogoreflection.Register(grpcSrv)

	return grpcSrv
}

func mtlsInterceptor(cquery ctypes.QueryClient) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				owner, err := utils.VerifyCertChain(ctx, mtls.State.PeerCertificates, cquery)
				if err != nil {
					return nil, fmt.Errorf("verify cert chain: %w", err)
				}

				if owner != nil {
					ctx = ContextWithOwner(ctx, owner)
				}
			}
		}

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
