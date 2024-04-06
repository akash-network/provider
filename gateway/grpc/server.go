package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"

	"github.com/akash-network/akash-api/go/grpc/gogoreflection"
	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	cmblog "github.com/tendermint/tendermint/libs/log"

	"github.com/akash-network/provider"
	"github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	_ providerv1.ProviderRPCServer = (*server)(nil)
	_ leasev1.LeaseRPCServer       = (*server)(nil)
)

type server struct {
	*providerV1
	*leaseV1
}

func Serve(ctx context.Context, endpoint string, certs []tls.Certificate, c provider.Client) error {
	group, err := fromctx.ErrGroupFromCtx(ctx)
	if err != nil {
		return err
	}

	grpcSrv := newServer(ctx, certs, c)

	log := fromctx.LogcFromCtx(ctx)

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

func newServer(ctx context.Context, certs []tls.Certificate, c provider.Client) *grpc.Server {
	// InsecureSkipVerify is set to true due to inability to use normal TLS verification
	// certificate validation and authentication performed later in mtlsHandler
	tlsConfig := &tls.Config{
		Certificates:       certs,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true, // nolint: gosec
		MinVersion:         tls.VersionTLS13,
	}

	cquery := MustQueryClientFromCtx(ctx)

	g := grpc.NewServer(
		grpc.Creds(
			credentials.NewTLS(tlsConfig),
		),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: false,
		}),
		grpc.ChainUnaryInterceptor(
			mtlsInterceptor(cquery),
			errorLogInterceptor(fromctx.LogcFromCtx(ctx)),
		),
	)

	s := &server{
		providerV1: &providerV1{
			ctx: ctx,
			c:   c,
		},
		leaseV1: &leaseV1{
			ctx: ctx,
			c:   c,
		},
	}

	providerv1.RegisterProviderRPCServer(g, s)
	leasev1.RegisterLeaseRPCServer(g, s)
	gogoreflection.Register(g)

	return g
}

func mtlsInterceptor(cquery ctypes.QueryClient) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				owner, err := utils.VerifyOwnerCert(ctx, mtls.State.PeerCertificates, "", x509.ExtKeyUsageClientAuth, cquery)
				if err != nil {
					return nil, fmt.Errorf("verify cert chain: %w", err)
				}

				if owner != nil {
					ctx = ContextWithOwner(ctx, owner)
				}
			}
		}

		return next(ctx, req)
	}
}

// TODO(andrewhare): Possibly replace this with
// https://github.com/grpc-ecosystem/go-grpc-middleware/tree/main/interceptors/logging
// to get full request/response logging?
func errorLogInterceptor(l cmblog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, i *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		resp, err := next(ctx, req)
		if err != nil {
			l.Error(i.FullMethod, "err", err)
		}

		return resp, err
	}
}
