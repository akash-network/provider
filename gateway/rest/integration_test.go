// nolint: err113
package rest

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"testing"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	akashmanifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	qmock "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	providertypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/akash-network/akash-api/go/testutil"

	"github.com/akash-network/provider"
	pcmock "github.com/akash-network/provider/cluster/mocks"
	clmocks "github.com/akash-network/provider/cluster/types/v1beta3/mocks"
	gwutils "github.com/akash-network/provider/gateway/utils"
	pmmock "github.com/akash-network/provider/manifest/mocks"
	pmock "github.com/akash-network/provider/mocks"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	testutilrest "github.com/akash-network/provider/testutil/rest"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/pconfig"
	"github.com/akash-network/provider/tools/pconfig/memory"
)

type testJwtSigner struct {
	key  cryptotypes.PrivKey
	addr sdk.Address
}

var _ ajwt.SignerI = (*testJwtSigner)(nil)

func (j testJwtSigner) GetAddress() sdk.Address {
	return j.addr
}

func (j testJwtSigner) Sign(_ string, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	res, err := j.key.Sign(msg)
	if err != nil {
		return nil, nil, err
	}

	return res, j.key.PubKey(), nil
}

func (j testJwtSigner) SignByAddress(_ sdk.Address, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	res, err := j.key.Sign(msg)
	if err != nil {
		return nil, nil, err
	}

	return res, j.key.PubKey(), nil
}

func Test_router_Status(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expected := &apclient.ProviderStatus{}
		pkey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey,
		}

		mocks := createMocks()
		mocks.pclient.On("Status", mock.Anything).Return(expected, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			opts := []apclient.ClientOption{apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			result, err := client.Status(context.Background())
			assert.NoError(t, err)
			assert.Equal(t, expected, result)
		})
		mocks.pclient.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		pkey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey,
		}

		mocks := createMocks()
		mocks.pclient.On("Status", mock.Anything).Return(nil, errors.New("oops"))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			opts := []apclient.ClientOption{apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			_, err = client.Status(context.Background())
			assert.Error(t, err)
		})
		mocks.pclient.AssertExpectations(t)
	})
}

func Test_router_Validate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expected := apclient.ValidateGroupSpecResult{
			MinBidPrice: testutil.AkashDecCoin(t, 200),
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		mocks := createMocks()
		mocks.pclient.On("Validate", mock.Anything, mock.Anything, mock.Anything).Return(expected, nil)

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			result, err := client.Validate(context.Background(), testutil.GroupSpec(t))
			assert.NoError(t, err)
			assert.Equal(t, expected, result)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			result, err = client.Validate(context.Background(), testutil.GroupSpec(t))
			assert.NoError(t, err)
			assert.Equal(t, expected, result)
		})
		mocks.pclient.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mocks := createMocks()

		mocks.pclient.On("Validate", mock.Anything, mock.Anything, mock.Anything).Return(apclient.ValidateGroupSpecResult{}, errors.New("oops"))
		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			_, err = client.Validate(context.Background(), dtypes.GroupSpec{})
			assert.Error(t, err)
			_, err = client.Validate(context.Background(), testutil.GroupSpec(t))
			assert.Error(t, err)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			_, err = client.Validate(context.Background(), dtypes.GroupSpec{})
			assert.Error(t, err)
			_, err = client.Validate(context.Background(), testutil.GroupSpec(t))
			assert.Error(t, err)
		})
		mocks.pclient.AssertExpectations(t)
	})
}

func Test_router_Manifest(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		did := testutil.DeploymentIDForAccount(t, caddr)
		mocks := createMocks()
		mocks.pmclient.On("Submit", mock.Anything, did, akashmanifest.Manifest(nil)).Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			err = client.SubmitManifest(context.Background(), did.DSeq, nil)
			assert.NoError(t, err)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			err = client.SubmitManifest(context.Background(), did.DSeq, nil)
			assert.NoError(t, err)
		})
		// mocks.pmclient.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		did := testutil.DeploymentIDForAccount(t, caddr)

		mocks := createMocks()
		mocks.pmclient.On("Submit", mock.Anything, did, akashmanifest.Manifest(nil)).Return(errors.New("ded"))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			err = client.SubmitManifest(context.Background(), did.DSeq, nil)
			assert.Error(t, err)

			opts = []apclient.ClientOption{apclient.WithQueryClient(mocks.qclient)}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			err = client.SubmitManifest(context.Background(), did.DSeq, nil)
			assert.Error(t, err)
		})
		mocks.pmclient.AssertExpectations(t)
	})
}

const testGroupName = "thegroup"
const testImageName = "theimage"
const testServiceName = "theservice"

func mockManifestGroups(m integrationMocks, leaseID mtypes.LeaseID) {
	status := make(map[string]*apclient.ServiceStatus)
	status[testServiceName] = &apclient.ServiceStatus{
		Name:               testServiceName,
		Available:          8,
		Total:              8,
		URIs:               nil,
		ObservedGeneration: 0,
		Replicas:           0,
		UpdatedReplicas:    0,
		ReadyReplicas:      0,
		AvailableReplicas:  0,
	}
	m.pcclient.On("LeaseStatus", mock.Anything, leaseID).Return(status, nil)
	m.pcclient.On("GetManifestGroup", mock.Anything, leaseID).Return(true, v2beta2.ManifestGroup{
		Name: testGroupName,
		Services: []v2beta2.ManifestService{{
			Name:  testServiceName,
			Image: testImageName,
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
				Service:      testServiceName,
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
	}, nil)
}

func Test_router_LeaseStatus(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		id := testutil.LeaseIDForAccount(t, caddr, paddr)
		mocks := createMocks()

		mockManifestGroups(mocks, id)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))
			expected := apclient.LeaseStatus{
				Services: map[string]*apclient.ServiceStatus{
					testServiceName: {
						Name:               testServiceName,
						Available:          8,
						Total:              8,
						URIs:               nil,
						ObservedGeneration: 0,
						Replicas:           0,
						UpdatedReplicas:    0,
						ReadyReplicas:      0,
						AvailableReplicas:  0,
					},
				},
				ForwardedPorts: nil,
				IPs:            nil,
			}

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err := client.LeaseStatus(context.Background(), id)
			assert.Equal(t, expected, status)
			assert.NoError(t, err)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err = client.LeaseStatus(context.Background(), id)
			assert.Equal(t, expected, status)
			assert.NoError(t, err)
		})
		mocks.pcclient.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		id := testutil.LeaseIDForAccount(t, caddr, paddr)
		mocks := createMocks()
		mocks.pcclient.On("LeaseStatus", mock.Anything, id).Return(nil, errors.New("ded"))

		mockManifestGroups(mocks, id)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err := client.LeaseStatus(context.Background(), id)
			assert.Error(t, err)
			assert.Equal(t, apclient.LeaseStatus{}, status)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err = client.LeaseStatus(context.Background(), id)
			assert.Error(t, err)
			assert.Equal(t, apclient.LeaseStatus{}, status)
		})
		mocks.pcclient.AssertExpectations(t)
	})
}

func Test_router_ServiceStatus(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)
		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		id := testutil.LeaseIDForAccount(t, caddr, paddr)

		expected := &apclient.ServiceStatus{}
		service := "svc"

		mocks := createMocks()
		mocks.pcclient.On("ServiceStatus", mock.Anything, id, service).Return(expected, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err := client.ServiceStatus(context.Background(), id, service)
			assert.NoError(t, err)
			assert.Equal(t, expected, status)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err = client.ServiceStatus(context.Background(), id, service)
			assert.NoError(t, err)
			assert.Equal(t, expected, status)
		})
		mocks.pcclient.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		pkey := testutil.Key(t)
		ckey := testutil.Key(t)

		keys := []cryptotypes.PrivKey{
			pkey, ckey,
		}

		paddr := sdk.AccAddress(pkey.PubKey().Address())
		caddr := sdk.AccAddress(ckey.PubKey().Address())

		id := testutil.LeaseIDForAccount(t, caddr, paddr)

		service := "svc"

		mocks := createMocks()
		mocks.pcclient.On("ServiceStatus", mock.Anything, id, service).Return(nil, errors.New("ded"))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		withServer(ctx, t, keys, mocks.pclient, mocks.qclient, nil, func(_ string, cquerier *certQuerier) {
			cert := testutil.Certificate(t, ckey, testutil.CertificateOptionMocks(mocks.qclient), testutil.CertificateOptionCache(cquerier))

			opts := []apclient.ClientOption{apclient.WithAuthCerts(cert.Cert), apclient.WithQueryClient(mocks.qclient)}
			client, err := apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err := client.ServiceStatus(context.Background(), id, service)
			assert.Nil(t, status)
			assert.Error(t, err)

			opts = []apclient.ClientOption{
				apclient.WithAuthJWTSigner(&testJwtSigner{
					key:  ckey,
					addr: caddr,
				}),
				apclient.WithQueryClient(mocks.qclient),
			}
			client, err = apclient.NewClient(context.Background(), paddr, opts...)
			assert.NoError(t, err)
			status, err = client.ServiceStatus(context.Background(), id, service)
			assert.Nil(t, status)
			assert.Error(t, err)
		})
		mocks.pcclient.AssertExpectations(t)
	})
}

type integrationMocks struct {
	pmclient       *pmmock.Client
	pcclient       *pcmock.Client
	pclient        *pmock.Client
	qclient        *qmock.QueryClient
	hostnameClient *clmocks.HostnameServiceClient
	clusterService *pcmock.Service
}

func createMocks() integrationMocks {
	var (
		pmclient       = &pmmock.Client{}
		pcclient       = &pcmock.Client{}
		pclient        = &pmock.Client{}
		qclient        = &qmock.QueryClient{}
		hostnameClient = &clmocks.HostnameServiceClient{}
		clusterService = &pcmock.Service{}
	)

	pclient.On("Manifest").Return(pmclient)
	pclient.On("Cluster").Return(pcclient)

	pclient.On("Hostname").Return(hostnameClient)
	pclient.On("ClusterService").Return(clusterService)

	return integrationMocks{
		pmclient:       pmclient,
		pcclient:       pcclient,
		pclient:        pclient,
		qclient:        qclient,
		hostnameClient: hostnameClient,
		clusterService: clusterService,
	}
}

type certQuerier struct {
	mtls     []tls.Certificate
	pstorage pconfig.Storage
}

func newCertQuerier(pstorage pconfig.Storage) *certQuerier {
	return &certQuerier{
		pstorage: pstorage,
	}
}

func (c certQuerier) AddAccountCertificate(ctx context.Context, addr sdk.Address, cert *x509.Certificate, pubkey crypto.PublicKey) error {
	return c.pstorage.AddAccountCertificate(ctx, addr, cert, pubkey)
}

func (c certQuerier) GetMTLS(_ context.Context) ([]tls.Certificate, error) {
	return c.mtls, nil
}

func (c certQuerier) GetCACerts(_ context.Context, _ string) ([]tls.Certificate, error) {
	return nil, nil
}

func (c certQuerier) GetAccountCertificate(ctx context.Context, address sdk.Address, serial *big.Int) (*x509.Certificate, crypto.PublicKey, error) {
	return c.pstorage.GetAccountCertificate(ctx, address, serial)
}

var _ gwutils.CertGetter = (*certQuerier)(nil)

func withServer(
	ctx context.Context,
	t testing.TB,
	keys []cryptotypes.PrivKey,
	pclient provider.Client,
	qclient *qmock.QueryClient,
	certs []tls.Certificate,
	fn func(string, *certQuerier),
) {
	t.Helper()

	addr := sdk.AccAddress(keys[0].PubKey().Address())

	router := newRouter(testutil.Logger(t), addr, pclient, map[interface{}]interface{}{})

	pstorage, err := memory.NewMemory()
	require.NoError(t, err)

	cquerier := newCertQuerier(pstorage)

	for _, key := range keys {
		pk := key.PubKey()
		addr := sdk.AccAddress(pk.Address())
		err := pstorage.AddAccount(ctx, addr, pk)
		require.NoError(t, err)
	}

	ctx = context.WithValue(ctx, fromctx.CtxKeyPersistentConfig, pstorage)
	ctx = context.WithValue(ctx, fromctx.CtxKeyAccountQuerier, pstorage)

	if len(certs) == 0 {
		crt := testutil.Certificate(
			t,
			keys[0],
			testutil.CertificateOptionDomains([]string{"localhost", "127.0.0.1"}),
			testutil.CertificateOptionMocks(qclient),
			testutil.CertificateOptionCache(cquerier),
		)

		certs = append(certs, crt.Cert...)
	}

	cquerier.mtls = certs

	server := testutilrest.NewServer(ctx, t, router, cquerier, "sni")
	defer server.Close()

	host := "https://" + server.Listener.Addr().String()
	qclient.On("Provider", mock.Anything, &providertypes.QueryProviderRequest{Owner: addr.String()}).
		Return(&providertypes.QueryProviderResponse{
			Provider: providertypes.Provider{
				Owner:   addr.String(),
				HostURI: host,
			},
		}, nil)

	fn(host, cquerier)
}
