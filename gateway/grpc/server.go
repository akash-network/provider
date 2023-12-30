package grpc

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/types/known/emptypb"

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
	providerv1.ProviderRPCServer
	ctx    context.Context
	client provider.StatusClient
}

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

	group := fromctx.ErrGroupFromCtx(ctx)
	log := fromctx.LogcFromCtx(ctx)

	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)), grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             30 * time.Second,
		PermitWithoutStream: false,
	}), grpc.ChainUnaryInterceptor(mtlsInterceptor()))

	providerv1.RegisterProviderRPCServer(grpcSrv, &grpcProviderV1{
		ctx:    ctx,
		client: client,
	})

	reflection.Register(grpcSrv)

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
					if len(certificates) != 1 {
						return nil, fmt.Errorf("tls: invalid certificate chain") // nolint: goerr113
					}

					cquery := QueryClientFromCtx(ctx)

					cert := certificates[0]

					// validation
					var owner sdk.Address
					if owner, err = sdk.AccAddressFromBech32(cert.Subject.CommonName); err != nil {
						return nil, fmt.Errorf("tls: invalid certificate's subject common name: %w", err)
					}

					// 1. CommonName in issuer and Subject must match and be as Bech32 format
					if cert.Subject.CommonName != cert.Issuer.CommonName {
						return nil, fmt.Errorf("tls: invalid certificate's issuer common name: %w", err)
					}

					// 2. serial number must be in
					if cert.SerialNumber == nil {
						return nil, fmt.Errorf("tls: invalid certificate serial number: %w", err)
					}

					// 3. look up certificate on chain
					var resp *ctypes.QueryCertificatesResponse
					resp, err = cquery.Certificates(
						ctx,
						&ctypes.QueryCertificatesRequest{
							Filter: ctypes.CertificateFilter{
								Owner:  owner.String(),
								Serial: cert.SerialNumber.String(),
								State:  "valid",
							},
						},
					)
					if err != nil {
						return nil, fmt.Errorf("tls: unable to fetch certificate from chain: %w", err)
					}
					if (len(resp.Certificates) != 1) || !resp.Certificates[0].Certificate.IsState(ctypes.CertificateValid) {
						return nil, errors.New("tls: attempt to use non-existing or revoked certificate") // nolint: goerr113
					}

					clientCertPool := x509.NewCertPool()
					clientCertPool.AddCert(cert)

					opts := x509.VerifyOptions{
						Roots:                     clientCertPool,
						CurrentTime:               time.Now(),
						KeyUsages:                 []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
						MaxConstraintComparisions: 0,
					}

					if _, err = cert.Verify(opts); err != nil {
						return nil, fmt.Errorf("tls: unable to verify certificate: %w", err)
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
	bus := fromctx.PubSubFromCtx(gm.ctx)

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
