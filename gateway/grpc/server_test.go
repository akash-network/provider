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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	types "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	qmock "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	v1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/testutil"

	cmocks "github.com/akash-network/provider/cluster/mocks"
	"github.com/akash-network/provider/cluster/types/v1beta3"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	ipmocks "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip/mocks"
	pmocks "github.com/akash-network/provider/mocks"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
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
		readClient     func(t *testing.T) *cmocks.ReadClient
		ipClient       func(t *testing.T) *ipmocks.Client
		run            func(context.Context, *testing.T, client)
	}{
		// GetStatus
		{
			desc: "GetStatus",
			providerClient: func() *pmocks.Client {
				var m pmocks.Client
				m.EXPECT().StatusV1(mock.Anything).Return(&providerv1.Status{}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.p.GetStatus(ctx, &emptypb.Empty{})
				assert.NoError(t, err)
			},
		},

		// ServiceStatus
		{
			desc: "ServiceStatus get manifest error",
			readClient: func(t *testing.T) *cmocks.ReadClient {
				var m cmocks.ReadClient

				m.EXPECT().GetManifestGroup(mock.Anything, mock.Anything).
					Return(false, v2beta2.ManifestGroup{}, errors.New("boom"))

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.ServiceStatus(ctx, &leasev1.ServiceStatusRequest{})
				assert.ErrorContains(t, err, "boom")
			},
		},
		{
			desc: "ServiceStatus no manifest group",
			readClient: func(t *testing.T) *cmocks.ReadClient {
				var m cmocks.ReadClient

				m.EXPECT().GetManifestGroup(mock.Anything, mock.Anything).
					Return(false, v2beta2.ManifestGroup{}, nil)

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.ServiceStatus(ctx, &leasev1.ServiceStatusRequest{})
				assert.ErrorContains(t, err, "lease does not exist")
			},
		},
		{
			desc: "ServiceStatus",
			readClient: func(t *testing.T) *cmocks.ReadClient {
				var m cmocks.ReadClient

				mgi := newManifestGroup(t)
				mgi.Services[0].Expose[0].IP = "1.2.3.4"
				mgi.Services[0].Expose[0].Global = true
				mgi.Services[0].Expose[0].ExternalPort = 8111

				m.EXPECT().GetManifestGroup(mock.Anything, mock.Anything).
					Return(true, mgi, nil)

				m.EXPECT().ForwardedPortStatus(mock.Anything, mock.Anything).
					Return(map[string][]ctypes.ForwardedPortStatus{
						serviceName(t): {
							{
								Host:         "host",
								Port:         1111,
								ExternalPort: 1112,
								Proto:        "test",
								Name:         serviceName(t),
							},
						},
					}, nil)

				m.EXPECT().LeaseStatus(mock.Anything, mock.Anything).
					Return(map[string]*ctypes.ServiceStatus{
						serviceName(t): {
							Name:               serviceName(t),
							Available:          111,
							Total:              222,
							URIs:               []string{"1", "2", "3"},
							ObservedGeneration: 1,
							Replicas:           2,
							UpdatedReplicas:    3,
							ReadyReplicas:      4,
							AvailableReplicas:  4,
						},
					}, nil)

				return &m
			},
			ipClient: func(t *testing.T) *ipmocks.Client {
				var m ipmocks.Client

				m.EXPECT().GetIPAddressStatus(mock.Anything, mock.Anything).
					Return([]ip.LeaseIPStatus{
						{
							Port:         3333,
							ExternalPort: 3334,
							ServiceName:  serviceName(t),
							IP:           "4.5.6.7",
							Protocol:     "test",
						},
					}, nil)

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				s, err := c.l.ServiceStatus(ctx, &leasev1.ServiceStatusRequest{
					Services: []string{serviceName(t)},
				})
				require.NoError(t, err)

				expected := v1.ServiceStatusResponse{
					Services: []v1.ServiceStatus{
						{
							Name: serviceName(t),
							Status: v1.LeaseServiceStatus{
								Available:          111,
								Total:              222,
								Uris:               []string{"1", "2", "3"},
								ObservedGeneration: 1,
								Replicas:           2,
								UpdatedReplicas:    3,
								ReadyReplicas:      4,
								AvailableReplicas:  4,
							},
							Ports: []v1.ForwarderPortStatus{
								{
									Host:         "host",
									Port:         1111,
									ExternalPort: 1112,
									Proto:        "test",
									Name:         serviceName(t),
								},
							},
							Ips: []v1.LeaseIPStatus{
								{
									Port:         3333,
									ExternalPort: 3334,
									Protocol:     "test",
									Ip:           "4.5.6.7",
								},
							},
						},
					},
				}

				assert.Equal(t, &expected, s)
			},
		},

		// StreamServiceLogs
		{
			desc: "StreamServiceLogs none",
			readClient: func(t *testing.T) *cmocks.ReadClient {
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
			readClient: func(t *testing.T) *cmocks.ReadClient {
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
			readClient: func(t *testing.T) *cmocks.ReadClient {
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
			readClient: func(t *testing.T) *cmocks.ReadClient {
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

		// StreamServiceStatus
		{
			desc: "StreamServiceStatus",
			readClient: func(t *testing.T) *cmocks.ReadClient {
				var m cmocks.ReadClient

				mgi := newManifestGroup(t)

				m.EXPECT().GetManifestGroup(mock.Anything, mock.Anything).
					Return(true, mgi, nil)

				m.EXPECT().LeaseStatus(mock.Anything, mock.Anything).
					Return(nil, nil)

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				interval := 500 * time.Millisecond

				ctx = metadata.AppendToOutgoingContext(ctx, "interval", interval.String())
				s, err := c.l.StreamServiceStatus(ctx, &leasev1.ServiceStatusRequest{})
				require.NoError(t, err)

				var (
					iterations = 3
					after      = time.After(interval * time.Duration(iterations))
					hits       int
				)

				for {
					select {
					case <-after:
						assert.Equal(t, iterations, hits)
						return
					default:
						_, err = s.Recv()
						if errors.Is(err, io.EOF) {
							break
						}
						require.NoError(t, err)
						hits++
					}
				}
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
				ip *ipmocks.Client
			)

			if c.providerClient != nil {
				pc = c.providerClient()
				defer pc.AssertExpectations(t)
			}
			if c.readClient != nil {
				rc = c.readClient(t)
				defer rc.AssertExpectations(t)
			}
			if c.ipClient != nil {
				ip = c.ipClient(t)
				defer ip.AssertExpectations(t)
			}

			s := NewServer(ctx,
				WithCerts(crt1.Cert),
				WithProviderClient(pc),
				WithClusterReadClient(rc),
				WithIPClient(ip),
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

func Test_getInfo(t *testing.T) {
	cases := []struct {
		desc     string
		manifest func(t *testing.T) v2beta2.ManifestGroup
		mgi      manifestGroupInfo
	}{
		{
			desc: "has leased ips",
			manifest: func(t *testing.T) v2beta2.ManifestGroup {
				mgi := newManifestGroup(t)

				mgi.Services[0].Expose[0].IP = "1.2.3.4"

				return mgi
			},
			mgi: manifestGroupInfo{
				hasLeasedIPs: true,
			},
		},
		{
			desc: "has forwarded ports",
			manifest: func(t *testing.T) v2beta2.ManifestGroup {
				mgi := newManifestGroup(t)

				mgi.Services[0].Expose[0].Global = true
				mgi.Services[0].Expose[0].ExternalPort = 8111

				return mgi
			},
			mgi: manifestGroupInfo{
				hasForwardedPorts: true,
			},
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			mgi := getInfo(c.manifest(t))
			assert.Equal(t, c.mgi, mgi)
		})
	}
}

func newManifestGroup(t *testing.T) v2beta2.ManifestGroup {
	t.Helper()

	return v2beta2.ManifestGroup{
		Name: serviceName(t),
		Services: []v2beta2.ManifestService{{
			Name:  serviceName(t),
			Image: t.Name() + "_image",
			Args:  nil,
			Env:   nil,
			Resources: v2beta2.Resources{
				CPU: v2beta2.ResourceCPU{
					Units: 1000,
				},
				Memory: v2beta2.ResourceMemory{
					Size: "3333",
				},
				Storage: v2beta2.ResourceStorage{
					{
						Name: "default",
						Size: "4444",
					},
				},
			},
			Count: 1,
			Expose: []v2beta2.ManifestServiceExpose{{
				Port:         8080,
				ExternalPort: 80,
				Proto:        "TCP",
				Service:      serviceName(t),
				Global:       true,
				Hosts:        []string{"hello.localhost"},
				HTTPOptions: v2beta2.ManifestServiceExposeHTTPOptions{
					MaxBodySize: 1,
					ReadTimeout: 2,
					SendTimeout: 3,
					NextTries:   4,
					NextTimeout: 5,
					NextCases:   nil,
				},
				IP:                     "",
				EndpointSequenceNumber: 1,
			}},
			Params: nil,
		}},
	}
}

func serviceName(t *testing.T) string {
	return t.Name() + "_service"
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
