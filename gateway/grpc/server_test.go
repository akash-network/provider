package grpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	manifestValidation "github.com/akash-network/akash-api/go/manifest/v2beta2"
	types "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	qmock "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/testutil"

	pmanifest "github.com/akash-network/provider/manifest"
	mmocks "github.com/akash-network/provider/manifest/mocks"
	"github.com/akash-network/provider/mocks"
)

type asserter interface {
	AssertExpectations(mock.TestingT) bool
}

type client struct {
	p providerv1.ProviderRPCClient
	l leasev1.LeaseRPCClient
}

func TestRPCs(t *testing.T) {
	var (
		qclient = &qmock.QueryClient{}
		com     = testutil.CertificateOptionMocks(qclient)
		cod     = testutil.CertificateOptionDomains([]string{"localhost", "127.0.0.1"})
	)

	var (
		crt1 = testutil.Certificate(t, testutil.AccAddress(t), com, cod)
		crt2 = testutil.Certificate(t, testutil.AccAddress(t), com, cod)
	)

	qclient.EXPECT().Certificates(mock.Anything, mock.Anything).Return(&types.QueryCertificatesResponse{
		Certificates: types.CertificatesResponse{
			types.CertificateResponse{
				Certificate: types.Certificate{
					State:  types.CertificateValid,
					Cert:   crt2.PEM.Cert,
					Pubkey: crt2.PEM.Pub,
				},
				Serial: crt2.Serial.String(),
			},
		},
	}, nil)

	cases := []struct {
		desc  string
		mocks func() (*mocks.Client, []asserter)
		run   func(context.Context, *testing.T, client)
	}{
		{
			desc: "GetStatus",
			mocks: func() (*mocks.Client, []asserter) {
				var c mocks.Client
				c.EXPECT().StatusV1(mock.Anything).Return(&providerv1.Status{}, nil)
				return &c, nil
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.p.GetStatus(ctx, &emptypb.Empty{})
				assert.NoError(t, err)
			},
		},
		{
			desc: "SendManifest",
			mocks: func() (*mocks.Client, []asserter) {
				var (
					c  mocks.Client
					mc mmocks.Client
				)

				mc.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(nil)
				c.EXPECT().Manifest().Return(&mc)

				return &c, []asserter{&mc}
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.SendManifest(ctx, &leasev1.SendManifestRequest{})
				assert.NoError(t, err)
			},
		},
		{
			desc: "SendManifest invalid",
			mocks: func() (*mocks.Client, []asserter) {
				var (
					c  mocks.Client
					mc mmocks.Client
				)

				mc.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(manifestValidation.ErrInvalidManifest)
				c.EXPECT().Manifest().Return(&mc)

				return &c, []asserter{&mc}
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.SendManifest(ctx, &leasev1.SendManifestRequest{})
				assert.ErrorContains(t, err, "invalid manifest")

				s, ok := status.FromError(err)
				assert.True(t, ok)
				assert.Equal(t, codes.InvalidArgument, s.Code())
			},
		},
		{
			desc: "SendManifest no lease",
			mocks: func() (*mocks.Client, []asserter) {
				var (
					c  mocks.Client
					mc mmocks.Client
				)

				mc.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(pmanifest.ErrNoLeaseForDeployment)
				c.EXPECT().Manifest().Return(&mc)

				return &c, []asserter{&mc}
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.SendManifest(ctx, &leasev1.SendManifestRequest{})
				assert.ErrorContains(t, err, "no lease")

				s, ok := status.FromError(err)
				assert.True(t, ok)
				assert.Equal(t, codes.NotFound, s.Code())
			},
		},
		{
			desc: "SendManifest internal",
			mocks: func() (*mocks.Client, []asserter) {
				var (
					c  mocks.Client
					mc mmocks.Client
				)

				mc.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(errors.New("boom"))
				c.EXPECT().Manifest().Return(&mc)

				return &c, []asserter{&mc}
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.SendManifest(ctx, &leasev1.SendManifestRequest{})
				assert.ErrorContains(t, err, "boom")

				s, ok := status.FromError(err)
				assert.True(t, ok)
				assert.Equal(t, codes.Internal, s.Code())
			},
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ctx = ContextWithQueryClient(ctx, qclient)

			mc, as := c.mocks()
			defer mc.AssertExpectations(t)

			for _, a := range as {
				defer a.AssertExpectations(t)
			}

			s := newServer(ctx, crt1.Cert, mc)
			defer s.Stop()

			l, err := net.Listen("tcp", ":0")
			require.NoError(t, err)

			go func() {
				require.NoError(t, s.Serve(l))
			}()

			tlsConfig := tls.Config{
				InsecureSkipVerify: true,
				Certificates:       crt2.Cert,
			}

			conn, err := grpc.DialContext(ctx, l.Addr().String(),
				grpc.WithTransportCredentials(credentials.NewTLS(&tlsConfig)))
			require.NoError(t, err)

			defer conn.Close()

			c.run(ctx, t, client{
				p: providerv1.NewProviderRPCClient(conn),
				l: leasev1.NewLeaseRPCClient(conn),
			})
		})
	}
}

func TestMTLS(t *testing.T) {
	var (
		qclient = &qmock.QueryClient{}
		com     = testutil.CertificateOptionMocks(qclient)
		cod     = testutil.CertificateOptionDomains([]string{"localhost", "127.0.0.1"})
	)

	crt := testutil.Certificate(t, testutil.AccAddress(t), com, cod)

	qclient.EXPECT().Certificates(mock.Anything, mock.Anything).Return(&types.QueryCertificatesResponse{
		Certificates: types.CertificatesResponse{
			types.CertificateResponse{
				Certificate: types.Certificate{
					State:  types.CertificateValid,
					Cert:   crt.PEM.Cert,
					Pubkey: crt.PEM.Pub,
				},
				Serial: crt.Serial.String(),
			},
		},
	}, nil)

	cases := []struct {
		desc        string
		cert        func(*testing.T) tls.Certificate
		errContains string
	}{
		{
			desc: "good cert",
			cert: func(*testing.T) tls.Certificate {
				return testutil.Certificate(t, testutil.AccAddress(t), com, cod).Cert[0]
			},
		},
		{
			desc: "empty chain",
			cert: func(*testing.T) tls.Certificate {
				return tls.Certificate{}
			},
			errContains: "empty chain",
		},
		{
			desc: "invalid subject",
			cert: func(t *testing.T) tls.Certificate {
				t.Helper()

				priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				require.NoError(t, err)

				template := x509.Certificate{
					SerialNumber: new(big.Int).SetInt64(time.Now().UTC().UnixNano()),
					Subject: pkix.Name{
						CommonName: "badcert",
					},
					BasicConstraintsValid: true,
				}

				certDer, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public(), priv)
				require.NoError(t, err)

				keyDer, err := x509.MarshalPKCS8PrivateKey(priv)
				require.NoError(t, err)

				certBytes := pem.EncodeToMemory(&pem.Block{
					Type:  types.PemBlkTypeCertificate,
					Bytes: certDer,
				})
				privBytes := pem.EncodeToMemory(&pem.Block{
					Type:  types.PemBlkTypeECPrivateKey,
					Bytes: keyDer,
				})

				cert, err := tls.X509KeyPair(certBytes, privBytes)
				require.NoError(t, err)

				return cert
			},
			errContains: "invalid certificate's subject",
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ctx = ContextWithQueryClient(ctx, qclient)

			var (
				m  mocks.Client
				mc mmocks.Client
			)

			mc.EXPECT().Submit(mock.Anything, mock.Anything, mock.Anything).Return(nil)
			m.EXPECT().Manifest().Return(&mc)

			s := newServer(ctx, crt.Cert, &m)
			defer s.Stop()

			l, err := net.Listen("tcp", ":0")
			require.NoError(t, err)

			go func() {
				require.NoError(t, s.Serve(l))
			}()

			tlsConfig := tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{c.cert(t)},
			}

			conn, err := grpc.DialContext(ctx, l.Addr().String(),
				grpc.WithTransportCredentials(credentials.NewTLS(&tlsConfig)))
			require.NoError(t, err)

			defer conn.Close()

			_, err = leasev1.NewLeaseRPCClient(conn).SendManifest(ctx, &leasev1.SendManifestRequest{})
			if c.errContains != "" {
				assert.ErrorContains(t, err, c.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
