package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	atls "github.com/akash-network/akash-api/go/util/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"

	"github.com/akash-network/akash-api/go/grpc/gogoreflection"
	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/provider"
	"github.com/akash-network/provider/cluster"
	"github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	_ providerv1.ProviderRPCServer = (*server)(nil)
	_ leasev1.LeaseRPCServer       = (*server)(nil)
	_ Server                       = (*grpcServer)(nil)
)

type Server interface {
	ServeOn(context.Context, string) error
}

type grpcServer struct {
	*grpc.Server
}

type server struct {
	ctx context.Context
	pc  provider.Client
	rc  cluster.ReadClient

	certs             []tls.Certificate
	providerClient    provider.Client
	clusterReadClient cluster.ReadClient
	ip                ip.Client

	clusterSettings map[any]any
}

func (s *grpcServer) ServeOn(ctx context.Context, addr string) error {
	group, err := fromctx.ErrGroupFromCtx(ctx)
	if err != nil {
		return err
	}

	log := fromctx.LogcFromCtx(ctx)

	group.Go(func() error {
		grpcLis, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}

		log.Info(fmt.Sprintf("grpc listening on \"%s\"", addr))

		return s.Serve(grpcLis)
	})

	group.Go(func() error {
		<-ctx.Done()

		s.GracefulStop()

		return ctx.Err()
	})

	return nil
}

type serverOpts struct {
	certs             []tls.Certificate
	providerClient    provider.Client
	clusterReadClient cluster.ReadClient
	clusterSettings   map[any]any
	ipClient          ip.Client
}

type opt func(*serverOpts)

func WithCerts(c []tls.Certificate) opt {
	return func(so *serverOpts) { so.certs = c }
}

func WithProviderClient(c provider.Client) opt {
	return func(so *serverOpts) { so.providerClient = c }
}

func WithClusterReadClient(c cluster.ReadClient) opt {
	return func(so *serverOpts) { so.clusterReadClient = c }
}

func WithClusterSettings(s map[any]any) opt {
	return func(so *serverOpts) { so.clusterSettings = s }
}

func WithIPClient(c ip.Client) opt {
	return func(so *serverOpts) { so.ipClient = c }
}

func NewServer(ctx context.Context, opts ...opt) Server {
	var o serverOpts
	for _, opt := range opts {
		opt(&o)
	}

	// InsecureSkipVerify is set to true due to inability to use normal TLS verification
	// certificate validation and authentication performed later in mtlsHandler
	tlsConfig := &tls.Config{
		Certificates:       o.certs,
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
		),
	)

	s := &server{
		ctx:             ctx,
		pc:              o.providerClient,
		rc:              o.clusterReadClient,
		clusterSettings: o.clusterSettings,
		ip:              o.ipClient,
	}

	providerv1.RegisterProviderRPCServer(g, s)
	leasev1.RegisterLeaseRPCServer(g, s)
	gogoreflection.Register(g)

	return &grpcServer{Server: g}
}

func mtlsInterceptor(cquery ctypes.QueryClient) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		if p, ok := peer.FromContext(ctx); ok {
			if mtls, ok := p.AuthInfo.(credentials.TLSInfo); ok {
				certificates := mtls.State.PeerCertificates

				if len(certificates) > 0 {
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
