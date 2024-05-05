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

	"github.com/akash-network/provider"

	sdk "github.com/cosmos/cosmos-sdk/types"
	kubeversion "k8s.io/apimachinery/pkg/version"

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
		providerClient func(*testing.T) *pmocks.Client
		clusterClient  func(*testing.T) *cmocks.Client
		ipClient       func(*testing.T) *ipmocks.Client
		run            func(context.Context, *testing.T, client)
	}{
		// GetStatus
		{
			desc: "GetStatus",
			providerClient: func(t *testing.T) *pmocks.Client {
				var m pmocks.Client
				m.EXPECT().StatusV1(mock.Anything).Return(&providerv1.Status{}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.p.GetStatus(ctx, &emptypb.Empty{})
				assert.NoError(t, err)
			},
		},

		// GetVersion
		{
			desc: "GetVersion",
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

				m.EXPECT().KubeVersion().Return(&kubeversion.Info{
					Major:        "1",
					Minor:        "2",
					GitVersion:   "3",
					GitTreeState: "4",
					BuildDate:    "5",
					GoVersion:    "6",
					Compiler:     "7",
					Platform:     "8",
				}, nil)

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				r, err := c.p.GetVersion(ctx, &emptypb.Empty{})
				require.NoError(t, err)

				assert.NotEmpty(t, r.Kube)
				assert.NotEmpty(t, r.Akash)
			},
		},

		// Validate
		{
			desc: "Validate error",
			providerClient: func(t *testing.T) *pmocks.Client {
				var m pmocks.Client

				m.EXPECT().Validate(mock.Anything, mock.Anything, mock.Anything).
					Return(provider.ValidateGroupSpecResult{}, errors.New("boom"))

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.p.Validate(ctx, &providerv1.ValidateRequest{})
				assert.ErrorContains(t, err, "boom")
			},
		},
		{
			desc: "Validate",
			providerClient: func(t *testing.T) *pmocks.Client {
				var m pmocks.Client

				m.EXPECT().Validate(mock.Anything, mock.Anything, mock.Anything).
					Return(provider.ValidateGroupSpecResult{
						MinBidPrice: sdk.DecCoin{
							Denom:  t.Name(),
							Amount: sdk.NewDec(111),
						},
					}, nil)

				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				res, err := c.p.Validate(ctx, &providerv1.ValidateRequest{})
				require.NoError(t, err)

				assert.Equal(t, t.Name(), res.MinBidPrice.Denom)
				assert.Equal(t, sdk.NewDec(111), res.MinBidPrice.Amount)
			},
		},

		// ServiceLogs
		{
			desc: "ServiceLogs none",
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client
				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]*v1beta3.ServiceLog{}, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.ServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				assert.ErrorContains(t, err, ErrNoRunningPods.Error())
			},
		},
		{
			desc: "ServiceLogs LeaseLogs err",
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client
				m.EXPECT().LeaseLogs(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("boom"))
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c client) {
				_, err := c.l.ServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				assert.ErrorContains(t, err, "boom")
			},
		},
		{
			desc: "ServiceLogs",
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
				resp, err := c.l.ServiceLogs(ctx, &leasev1.ServiceLogsRequest{})
				require.NoError(t, err)

				expected := leasev1.ServiceLogsResponse{
					Services: []*leasev1.ServiceLogs{
						{Name: "one", Logs: []byte("1_1")},
						{Name: "one", Logs: []byte("1_2")},
						{Name: "one", Logs: []byte("1_3")},
						{Name: "two", Logs: []byte("2_1")},
						{Name: "two", Logs: []byte("2_2")},
						{Name: "two", Logs: []byte("2_3")},
						{Name: "three", Logs: []byte("3_1")},
						{Name: "three", Logs: []byte("3_2")},
						{Name: "three", Logs: []byte("3_3")},
					},
				}

				assert.EqualValues(t, &expected, resp)
			},
		},

		// ServiceStatus
		{
			desc: "ServiceStatus get manifest error",
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client
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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client
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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
			clusterClient: func(t *testing.T) *cmocks.Client {
				var m cmocks.Client

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
				cc *cmocks.Client
				ip *ipmocks.Client
			)

			if c.providerClient != nil {
				pc = c.providerClient(t)
				defer pc.AssertExpectations(t)
			}
			if c.clusterClient != nil {
				cc = c.clusterClient(t)
				defer cc.AssertExpectations(t)
			}
			if c.ipClient != nil {
				ip = c.ipClient(t)
				defer ip.AssertExpectations(t)
			}

			s := NewServer(ctx,
				WithCerts(crt1.Cert),
				WithProviderClient(pc),
				WithClusterClient(cc),
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
		certs       func() []tls.Certificate
		errContains string
	}{
		{
			desc: "good cert",
			certs: func() []tls.Certificate {
				return crt.Cert
			},
		},
		{
			desc: "empty chain",
			certs: func() []tls.Certificate {
				return nil
			},
			errContains: "too many peer certificates",
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
				Certificates:       c.certs(),
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
