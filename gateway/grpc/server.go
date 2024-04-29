package grpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	atls "github.com/akash-network/akash-api/go/util/tls"
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

func NewServer(ctx context.Context, endpoint string, certs []tls.Certificate, client provider.StatusClient) error {
	// InsecureSkipVerify is set to true due to inability to use normal TLS verification
	// certificate validation and authentication performed later in mtlsHandler
	tlsConfig := &tls.Config{
		Certificates:       certs,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true, // nolint: gosec
		MinVersion:         tls.VersionTLS13,
	}

	group, err := fromctx.ErrGroupFromCtx(ctx)
	if err != nil {
		return err
	}

	log := fromctx.LogcFromCtx(ctx)

	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             30 * time.Second,
		PermitWithoutStream: false,
	}), grpc.ChainUnaryInterceptor(mtlsInterceptor()))

	pRPC := &grpcProviderV1{
		ctx:    ctx,
		client: client,
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

func mtlsInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				certificates := mtls.State.PeerCertificates

				if len(certificates) > 0 {
					cquery := QueryClientFromCtx(ctx)

					owner, _, err := atls.ValidatePeerCertificates(ctx, cquery, certificates, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
					if err != nil {
						return nil, err
					}

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
