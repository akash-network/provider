package grpc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"

	types "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	qmock "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/testutil"

	cmocks "github.com/akash-network/provider/cluster/mocks"
	"github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/mocks"
	pmocks "github.com/akash-network/provider/mocks"
)

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
		desc           string
		providerClient func() *pmocks.Client
		readClient     func() *cmocks.ReadClient
		run            func(context.Context, *testing.T, client)
	}{
		{
			desc: "GetStatus",
			providerClient: func() *mocks.Client {
				var m mocks.Client
				m.EXPECT().StatusV1(mock.Anything).Return(&providerv1.Status{}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.p.GetStatus(ctx, &emptypb.Empty{})
				assert.NoError(t, err)
			},
		},
		{
			desc: "StreamServiceLogs none",
			readClient: func() *cmocks.ReadClient {
				var m cmocks.ReadClient
				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]*v1beta3.ServiceLog{}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				s, err := c.l.StreamServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				require.NoError(t, err)

				_, err = s.Recv()
				assert.ErrorContains(t, err, ErrNoRunningPods.Error())
			},
		},
		{
			desc: "StreamServiceLogs LeaseLogs err",
			readClient: func() *cmocks.ReadClient {
				var m cmocks.ReadClient
				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("boom"))
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				s, err := c.l.StreamServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				require.NoError(t, err)

				_, err = s.Recv()
				assert.ErrorContains(t, err, "boom")
			},
		},
		{
			desc: "StreamServiceLogs one",
			readClient: func() *cmocks.ReadClient {
				var m cmocks.ReadClient

				stream := io.NopCloser(strings.NewReader("1\n2\n3"))

				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]*v1beta3.ServiceLog{
						{
							Name:    "one",
							Stream:  stream,
							Scanner: bufio.NewScanner(stream),
						},
					}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				s, err := c.l.StreamServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				require.NoError(t, err)

				var (
					expected = []string{"1", "2", "3"}
					actual   = make([]string, 0, len(expected))
				)

				for {
					r, err := s.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					require.NoError(t, err)
					actual = append(actual, string(r.Services[0].Logs))
				}

				assert.Equal(t, expected, actual)
			},
		},
		{
			desc: "StreamServiceLogs multiple",
			readClient: func() *cmocks.ReadClient {
				var m cmocks.ReadClient

				var (
					stream1 = io.NopCloser(strings.NewReader("1_1\n1_2\n1_3"))
					stream2 = io.NopCloser(strings.NewReader("2_1\n2_2\n2_3"))
					stream3 = io.NopCloser(strings.NewReader("3_1\n3_2\n3_3"))
				)

				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]*v1beta3.ServiceLog{
						{
							Name:    "one",
							Stream:  stream1,
							Scanner: bufio.NewScanner(stream1),
						},
						{
							Name:    "two",
							Stream:  stream2,
							Scanner: bufio.NewScanner(stream2),
						},
						{
							Name:    "three",
							Stream:  stream3,
							Scanner: bufio.NewScanner(stream3),
						},
					}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				s, err := c.l.StreamServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				require.NoError(t, err)

				var (
					expected = map[string][]string{
						"one":   {"1_1", "1_2", "1_3"},
						"two":   {"2_1", "2_2", "2_3"},
						"three": {"3_1", "3_2", "3_3"},
					}
					actual = make(map[string][]string)
				)

				for {
					r, err := s.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					require.NoError(t, err)

					for _, s := range r.Services {
						actual[s.Name] = append(actual[s.Name], string(s.Logs))
					}
				}

				assert.Equal(t, expected, actual)
			},
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ctx = ContextWithQueryClient(ctx, qclient)

			var (
				pc *pmocks.Client
				rc *cmocks.ReadClient
			)

			if c.providerClient != nil {
				pc = c.providerClient()
				defer pc.AssertExpectations(t)
			}
			if c.readClient != nil {
				rc = c.readClient()
				defer rc.AssertExpectations(t)
			}

			s := NewServer(ctx,
				WithCerts(crt1.Cert),
				WithProviderClient(pc),
				WithClusterReadClient(rc),
			).(*grpcServer)

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
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ctx = ContextWithQueryClient(ctx, qclient)

			var m pmocks.Client
			m.EXPECT().StatusV1(mock.Anything).Return(&providerv1.Status{}, nil)

			s := NewServer(ctx,
				WithCerts(crt.Cert),
				WithProviderClient(&m),
			).(*grpcServer)

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

			_, err = providerv1.NewProviderRPCClient(conn).GetStatus(ctx, &emptypb.Empty{})
			if c.errContains != "" {
				assert.ErrorContains(t, err, c.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
