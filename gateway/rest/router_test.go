package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeVersion "k8s.io/apimachinery/pkg/version"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	manifestValidation "pkg.akt.dev/go/manifest/v2beta3"
	qmock "pkg.akt.dev/go/mocks/node/client"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"
	ajwt "pkg.akt.dev/go/util/jwt"

	kubeclienterrors "github.com/akash-network/provider/cluster/kube/errors"
	pmock "github.com/akash-network/provider/mocks/client"
	pcmock "github.com/akash-network/provider/mocks/cluster"
	clmocks "github.com/akash-network/provider/mocks/cluster/types"
	pmmock "github.com/akash-network/provider/mocks/manifest"
	"github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/version"
)

const (
	testSDL     = "../../testdata/sdl/simple.yaml"
	serviceName = "database"
)

type routerTestAuth int

const (
	routerTestAuthNone routerTestAuth = iota
	routerTestAuthCert
	routerTestAuthJWT
)

var errGeneric = errors.New("generic test error")

type fakeKubernetesStatusError struct {
	status metav1.Status
}

func (fkse fakeKubernetesStatusError) Status() metav1.Status {
	return fkse.status
}

func (fkse fakeKubernetesStatusError) Error() string {
	return "fake error"
}

type routerTest struct {
	ckey           cryptotypes.PrivKey
	pkey           cryptotypes.PrivKey
	pmclient       *pmmock.Client
	pcclient       *pcmock.Client
	pclient        *pmock.Client
	qclient        *qmock.QueryClient
	clusterService *pcmock.Service
	hostnameClient *clmocks.HostnameServiceClient
	gwclient       apclient.Client
	host           *url.URL
}

// TODO - add some tests in here to make sure the IP operator calls work as intended
func runRouterTest(t *testing.T, authTypes []routerTestAuth, fn func(*routerTest, http.Header)) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mocks := createMocks()

	mf := &routerTest{
		ckey:           testutil.Key(t),
		pkey:           testutil.Key(t),
		pmclient:       mocks.pmclient,
		pcclient:       mocks.pcclient,
		pclient:        mocks.pclient,
		qclient:        mocks.qclient,
		hostnameClient: mocks.hostnameClient,
		clusterService: mocks.clusterService,
	}

	keys := []cryptotypes.PrivKey{
		mf.pkey, mf.ckey,
	}

	for _, authType := range authTypes {
		withServer(ctx, t, keys, mocks.pclient, mocks, func(host string, cquerier *certQuerier) {
			var err error
			mf.host, err = url.Parse(host)
			require.NoError(t, err)

			var opts []apclient.ClientOption

			hdr := make(http.Header)

			addr := sdk.AccAddress(mf.ckey.PubKey().Address())

			switch authType {
			case routerTestAuthCert:
				cert := testutil.Certificate(t, addr, testutil.CertificateOptionMocks(mocks.cmocks), testutil.CertificateOptionCache(cquerier))
				opts = append(opts,
					apclient.WithAuthCerts(cert.Cert))
			case routerTestAuthJWT:
				signer := &testJwtSigner{
					key:  mf.ckey,
					addr: addr,
				}

				opts = append(opts, apclient.WithAuthJWTSigner(signer))

				now := time.Now()

				claims := ajwt.Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						Issuer:    addr.String(),
						IssuedAt:  jwt.NewNumericDate(now),
						NotBefore: jwt.NewNumericDate(now),
						ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
					},
					Version: "v1",
					Leases:  ajwt.Leases{Access: ajwt.AccessTypeFull},
				}

				tok := jwt.NewWithClaims(ajwt.SigningMethodES256K, &claims)

				tokString, err := tok.SignedString(signer)
				require.NoError(t, err)

				hdr.Set("Authorization", fmt.Sprintf("Bearer %s", tokString))
			}

			opts = append(opts, apclient.WithProviderURL(host), apclient.WithCertQuerier(cquerier))

			mf.gwclient, err = apclient.NewClient(ctx, sdk.AccAddress(mf.pkey.PubKey().Address()), opts...)
			require.NoError(t, err)
			require.NotNil(t, mf.gwclient)

			fn(mf, hdr)
		})
	}
}

func testCertHelper(t *testing.T, test *routerTest) {
	test.pmclient.On(
		"Submit",
		mock.Anything,
		mock.AnythingOfType("dtypes.DeploymentID"),
		mock.AnythingOfType("v2beta2.Manifest"),
	).Return(nil)

	dseq := uint64(testutil.RandRangeInt(1, 1000)) // nolint: gosec

	uri, err := apclient.MakeURI(test.host, apclient.SubmitManifestPath(dseq))
	require.NoError(t, err)

	sdl, err := sdl.ReadFile(testSDL)
	require.NoError(t, err)

	mani, err := sdl.Manifest()
	require.NoError(t, err)

	buf, err := json.Marshal(mani)
	require.NoError(t, err)

	req, err := http.NewRequest("PUT", uri, bytes.NewBuffer(buf))
	require.NoError(t, err)

	req.Header.Set("Content-Type", contentTypeJSON)

	rCl := test.gwclient.NewReqClient(context.Background())
	resp, err := rCl.Do(req)
	require.NoError(t, err)

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRouteNotActiveClientCert(t *testing.T) {
	mocks := createMocks()

	mf := &routerTest{
		ckey:     testutil.Key(t),
		pkey:     testutil.Key(t),
		pmclient: mocks.pmclient,
		pcclient: mocks.pcclient,
		pclient:  mocks.pclient,
		qclient:  mocks.qclient,
	}

	keys := []cryptotypes.PrivKey{
		mf.pkey, mf.ckey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	withServer(ctx, t, keys, mocks.pclient, mocks, func(host string, cquerier *certQuerier) {
		var err error
		mf.host, err = url.Parse(host)
		require.NoError(t, err)

		mf.gwclient, err = apclient.NewClient(context.Background(), sdk.AccAddress(mf.pkey.PubKey().Address()), apclient.WithProviderURL(host), apclient.WithCertQuerier(cquerier))
		require.NoError(t, err)
		require.NotNil(t, mf.gwclient)

		testCertHelper(t, mf)
	})
}

func TestRouteExpiredClientCert(t *testing.T) {
	mocks := createMocks()

	mf := &routerTest{
		ckey:     testutil.Key(t),
		pkey:     testutil.Key(t),
		pmclient: mocks.pmclient,
		pcclient: mocks.pcclient,
		pclient:  mocks.pclient,
		qclient:  mocks.qclient,
	}

	keys := []cryptotypes.PrivKey{
		mf.pkey, mf.ckey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	withServer(ctx, t, keys, mocks.pclient, mocks, func(host string, cquerier *certQuerier) {
		var err error
		mf.host, err = url.Parse(host)
		require.NoError(t, err)

		mf.gwclient, err = apclient.NewClient(context.Background(), sdk.AccAddress(mf.pkey.PubKey().Address()), apclient.WithProviderURL(host), apclient.WithCertQuerier(cquerier))
		require.NoError(t, err)
		require.NotNil(t, mf.gwclient)

		testCertHelper(t, mf)
	})
}

func TestRouteNotActiveServerCert(t *testing.T) {
	mocks := createMocks()

	mf := &routerTest{
		ckey:     testutil.Key(t),
		pkey:     testutil.Key(t),
		pmclient: mocks.pmclient,
		pcclient: mocks.pcclient,
		pclient:  mocks.pclient,
		qclient:  mocks.qclient,
	}

	keys := []cryptotypes.PrivKey{
		mf.pkey, mf.ckey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	withServer(ctx, t, keys, mocks.pclient, mocks, func(host string, cquerier *certQuerier) {
		var err error
		mf.host, err = url.Parse(host)
		require.NoError(t, err)

		mf.gwclient, err = apclient.NewClient(context.Background(), sdk.AccAddress(mf.pkey.PubKey().Address()), apclient.WithProviderURL(host), apclient.WithCertQuerier(cquerier))
		require.NoError(t, err)
		require.NotNil(t, mf.gwclient)

		testCertHelper(t, mf)
	})
}

func TestRouteExpiredServerCert(t *testing.T) {
	mocks := createMocks()

	mf := &routerTest{
		ckey:     testutil.Key(t),
		pkey:     testutil.Key(t),
		pmclient: mocks.pmclient,
		pcclient: mocks.pcclient,
		pclient:  mocks.pclient,
		qclient:  mocks.qclient,
	}

	keys := []cryptotypes.PrivKey{
		mf.pkey, mf.ckey,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	withServer(ctx, t, keys, mocks.pclient, mocks, func(host string, cquerier *certQuerier) {
		var err error
		mf.host, err = url.Parse(host)
		require.NoError(t, err)

		mf.gwclient, err = apclient.NewClient(context.Background(), sdk.AccAddress(mf.pkey.PubKey().Address()), apclient.WithProviderURL(host), apclient.WithCertQuerier(cquerier))
		require.NoError(t, err)
		require.NotNil(t, mf.gwclient)

		testCertHelper(t, mf)
	})
}

func TestRouteDoesNotExist(t *testing.T) {
	runRouterTest(t, []routerTestAuth{}, func(test *routerTest, hdr http.Header) {
		uri, err := apclient.MakeURI(test.host, "foobar")
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)

		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestRouteVersionOK(t *testing.T) {
	runRouterTest(t, []routerTestAuth{}, func(test *routerTest, hdr http.Header) {
		// these are set at build time
		version.Version = "akashTest"
		version.Commit = "testCommit"
		version.BuildTags = "testTags"

		status := versionInfo{
			Akash: version.Info{
				Version:          "akashTest",
				GitCommit:        "testCommit",
				BuildTags:        "testTags",
				GoVersion:        "", // ignored in comparison
				CosmosSdkVersion: "", // ignored in comparison
			},
			Kube: &kubeVersion.Info{
				Major:        "1",
				Minor:        "2",
				GitVersion:   "3",
				GitCommit:    "4",
				GitTreeState: "5",
				BuildDate:    "6",
				GoVersion:    "7",
				Compiler:     "8",
				Platform:     "9",
			},
		}

		test.pcclient.On("KubeVersion").Return(status.Kube, nil)

		uri, err := apclient.MakeURI(test.host, apclient.VersionPath())
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr
		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var data versionInfo
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&data)
		require.NoError(t, err)
		require.Equal(t, status.Kube, data.Kube)
		require.Equal(t, status.Akash.Version, data.Akash.Version)
		require.Equal(t, status.Akash.GitCommit, data.Akash.GitCommit)
		require.Equal(t, status.Akash.BuildTags, data.Akash.BuildTags)
	})
}

func TestRouteStatusOK(t *testing.T) {
	runRouterTest(t, []routerTestAuth{}, func(test *routerTest, hdr http.Header) {
		status := &apclient.ProviderStatus{
			Cluster:               nil,
			Bidengine:             nil,
			Manifest:              nil,
			ClusterPublicHostname: "foobar",
		}

		test.pclient.On("Status", mock.Anything).Return(status, nil)

		uri, err := apclient.MakeURI(test.host, apclient.StatusPath())
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		data := make(map[string]interface{})
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&data)
		require.NoError(t, err)
		cph, ok := data["cluster_public_hostname"].(string)
		require.True(t, ok)
		require.Equal(t, cph, "foobar")
	})
}

func TestRouteStatusFails(t *testing.T) {
	runRouterTest(t, []routerTestAuth{}, func(test *routerTest, hdr http.Header) {
		test.pclient.On("Status", mock.Anything).Return(nil, errGeneric)

		uri, err := apclient.MakeURI(test.host, apclient.StatusPath())
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)
		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^generic test error(?s:.)*$", string(data))
	})
}

func TestRouteValidateOK(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		validate := apclient.ValidateGroupSpecResult{
			MinBidPrice: testutil.AkashDecCoin(t, 200),
		}

		test.pclient.On("Validate", mock.Anything, mock.Anything, mock.Anything).Return(validate, nil)

		uri, err := apclient.MakeURI(test.host, apclient.ValidatePath())
		require.NoError(t, err)

		gspec := testutil.GroupSpec(t)
		bgspec, err := json.Marshal(&gspec)
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, bytes.NewReader(bgspec))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		data := make(map[string]interface{})
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&data)
		require.NoError(t, err)
	})
}

func TestRouteValidateUnauthorized(t *testing.T) {
	runRouterTest(t, []routerTestAuth{}, func(test *routerTest, hdr http.Header) {
		validate := apclient.ValidateGroupSpecResult{
			MinBidPrice: testutil.AkashDecCoin(t, 200),
		}

		test.pclient.On("Validate", mock.Anything, mock.Anything, mock.Anything).Return(validate, nil)

		uri, err := apclient.MakeURI(test.host, apclient.ValidatePath())
		require.NoError(t, err)

		gspec := testutil.GroupSpec(t)
		bgspec, err := json.Marshal(&gspec)
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, bytes.NewReader(bgspec))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

func TestRouteValidateFails(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		test.pclient.On("Validate", mock.Anything, mock.Anything, mock.Anything).Return(apclient.ValidateGroupSpecResult{}, errGeneric)

		uri, err := apclient.MakeURI(test.host, apclient.ValidatePath())
		require.NoError(t, err)

		gspec := testutil.GroupSpec(t)
		bgspec, err := json.Marshal(&gspec)
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, bytes.NewReader(bgspec))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)
		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^generic test error(?s:.)*$", string(data))
	})
}

func TestRouteValidateFailsEmptyBody(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		test.pclient.On("Validate", mock.Anything, mock.Anything).Return(apclient.ValidateGroupSpecResult{}, errGeneric)

		uri, err := apclient.MakeURI(test.host, apclient.ValidatePath())
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr
		req.Header.Set("Content-Type", contentTypeJSON)
		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "empty payload", string(data))
	})
}

func TestRoutePutManifestOK(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000)) // nolint: gosec
		test.pmclient.On(
			"Submit",
			mock.Anything,
			dtypes.DeploymentID{
				Owner: caddr.String(),
				DSeq:  dseq,
			},
			mock.AnythingOfType("v2beta3.Manifest"),
		).Return(nil)

		uri, err := apclient.MakeURI(test.host, apclient.SubmitManifestPath(dseq))
		require.NoError(t, err)

		sdl, err := sdl.ReadFile(testSDL)
		require.NoError(t, err)

		mani, err := sdl.Manifest()
		require.NoError(t, err)

		buf, err := json.Marshal(mani)
		require.NoError(t, err)

		req, err := http.NewRequest("PUT", uri, bytes.NewBuffer(buf))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, string(data), "")
	})
}

func TestRoutePutInvalidManifest(t *testing.T) {
	_ = dtypes.DeploymentID{}
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000)) // nolint: gosec
		test.pmclient.On("Submit",
			mock.Anything,
			dtypes.DeploymentID{
				Owner: caddr.String(),
				DSeq:  dseq,
			},

			mock.AnythingOfType("v2beta3.Manifest"),
		).Return(manifestValidation.ErrInvalidManifest)

		uri, err := apclient.MakeURI(test.host, apclient.SubmitManifestPath(dseq))
		require.NoError(t, err)

		sdl, err := sdl.ReadFile(testSDL)
		require.NoError(t, err)

		mani, err := sdl.Manifest()
		require.NoError(t, err)

		buf, err := json.Marshal(mani)
		require.NoError(t, err)

		req, err := http.NewRequest("PUT", uri, bytes.NewBuffer(buf))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^invalid manifest(?s:.)*$", string(data))
	})
}

func mockManifestGroupsForRouterTest(rt *routerTest, leaseID mtypes.LeaseID) {
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
	rt.pcclient.On("LeaseStatus", mock.Anything, leaseID).Return(status, nil)
	rt.pcclient.On("GetManifestGroup", mock.Anything, leaseID).Return(true, v2beta2.ManifestGroup{
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

func TestRouteLeaseStatusOk(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()
		leaseID.Provider = paddr.String()
		mockManifestGroupsForRouterTest(test, leaseID)

		uri, err := apclient.MakeURI(test.host, apclient.LeaseStatusPath(leaseID))
		require.NoError(t, err)

		parsedSDL, err := sdl.ReadFile(testSDL)
		require.NoError(t, err)

		mani, err := parsedSDL.Manifest()
		require.NoError(t, err)

		buf, err := json.Marshal(mani)
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, bytes.NewBuffer(buf))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		data := make(map[string]interface{})
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&data)
		require.NoError(t, err)
	})
}

func TestRouteLeaseNotInKubernetes(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()
		leaseID.Provider = paddr.String()

		kubeStatus := fakeKubernetesStatusError{
			status: metav1.Status{
				TypeMeta: metav1.TypeMeta{},
				ListMeta: metav1.ListMeta{},
				Status:   "",
				Message:  "",
				Reason:   metav1.StatusReasonNotFound,
				Details:  nil,
				Code:     0,
			},
		}
		test.pcclient.On("LeaseStatus", mock.Anything, leaseID).Return(nil, kubeStatus)
		mockManifestGroupsForRouterTest(test, leaseID)

		uri, err := apclient.MakeURI(test.host, apclient.LeaseStatusPath(leaseID))
		require.NoError(t, err)

		parsedSDL, err := sdl.ReadFile(testSDL)
		require.NoError(t, err)

		mani, err := parsedSDL.Manifest()
		require.NoError(t, err)

		buf, err := json.Marshal(mani)
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, bytes.NewBuffer(buf))
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestRouteLeaseStatusErr(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()
		leaseID.Provider = paddr.String()
		test.pcclient.On("LeaseStatus", mock.Anything, leaseID).Return(nil, errGeneric)
		mockManifestGroupsForRouterTest(test, leaseID)

		uri, err := apclient.MakeURI(test.host, apclient.LeaseStatusPath(leaseID))
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^generic test error(?s:.)*$", string(data))
	})
}

func TestRouteServiceStatusOK(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000))    // nolint: gosec
		oseq := uint32(testutil.RandRangeInt(2000, 3000)) // nolint: gosec
		gseq := uint32(testutil.RandRangeInt(4000, 5000)) // nolint: gosec

		status := &apclient.ServiceStatus{
			Name:               "",
			Available:          0,
			Total:              0,
			URIs:               nil,
			ObservedGeneration: 0,
			Replicas:           0,
			UpdatedReplicas:    0,
			ReadyReplicas:      0,
			AvailableReplicas:  0,
		}
		test.pcclient.On("ServiceStatus", mock.Anything, mtypes.LeaseID{
			Owner:    caddr.String(),
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}, serviceName).Return(status, nil)

		lid := mtypes.LeaseID{
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}

		uri, err := apclient.MakeURI(test.host, apclient.ServiceStatusPath(lid, serviceName))
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		data := make(map[string]interface{})
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&data)
		require.NoError(t, err)
	})
}

func TestRouteServiceStatusNoDeployment(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000))    // nolint: gosec
		oseq := uint32(testutil.RandRangeInt(2000, 3000)) // nolint: gosec
		gseq := uint32(testutil.RandRangeInt(4000, 5000)) // nolint: gosec

		test.pcclient.On("ServiceStatus", mock.Anything, mtypes.LeaseID{
			Owner:    caddr.String(),
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}, serviceName).Return(nil, kubeclienterrors.ErrNoDeploymentForLease)

		lid := mtypes.LeaseID{
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}

		uri, err := apclient.MakeURI(test.host, apclient.ServiceStatusPath(lid, serviceName))
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^kube: no deployment(?s:.)*$", string(data))
	})
}

func TestRouteServiceStatusKubernetesNotFound(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthCert, routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000))    // nolint: gosec
		oseq := uint32(testutil.RandRangeInt(2000, 3000)) // nolint: gosec
		gseq := uint32(testutil.RandRangeInt(4000, 5000)) // nolint: gosec

		kubeStatus := fakeKubernetesStatusError{
			status: metav1.Status{
				TypeMeta: metav1.TypeMeta{},
				ListMeta: metav1.ListMeta{},
				Status:   "",
				Message:  "",
				Reason:   metav1.StatusReasonNotFound,
				Details:  nil,
				Code:     0,
			},
		}

		test.pcclient.On("ServiceStatus", mock.Anything, mtypes.LeaseID{
			Owner:    caddr.String(),
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}, serviceName).Return(nil, kubeStatus)

		lid := mtypes.LeaseID{
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}

		uri, err := apclient.MakeURI(test.host, apclient.ServiceStatusPath(lid, serviceName))
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)

		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^fake error(?s:.)*$", string(data))
	})
}

func TestRouteServiceStatusError(t *testing.T) {
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, func(test *routerTest, hdr http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		paddr := sdk.AccAddress(test.pkey.PubKey().Address())

		dseq := uint64(testutil.RandRangeInt(1, 1000))    // nolint: gosec
		oseq := uint32(testutil.RandRangeInt(2000, 3000)) // nolint: gosec
		gseq := uint32(testutil.RandRangeInt(4000, 5000)) // nolint: gosec

		test.pcclient.On("ServiceStatus", mock.Anything, mtypes.LeaseID{
			Owner:    caddr.String(),
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}, serviceName).Return(nil, errGeneric)

		lid := mtypes.LeaseID{
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		}

		uri, err := apclient.MakeURI(test.host, apclient.ServiceStatusPath(lid, serviceName))
		require.NoError(t, err)

		req, err := http.NewRequest("GET", uri, nil)
		require.NoError(t, err)
		req.Header = hdr

		req.Header.Set("Content-Type", contentTypeJSON)

		rCl := test.gwclient.NewReqClient(context.Background())
		resp, err := rCl.Do(req)
		require.NoError(t, err)

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Regexp(t, "^generic test error(?s:.)*$", string(data))
	})
}
