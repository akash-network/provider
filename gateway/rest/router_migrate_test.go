package rest

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"

	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

func TestRouteMigrateHostnameDoesNotExist(t *testing.T) {
	testFn := func(test *routerTest, _ http.Header) {
		const dseq = uint64(33)
		const gseq = uint32(34)

		test.clusterService.On("FindActiveLease", mock.Anything, mock.Anything, dseq, gseq).Return(false, mtypes.LeaseID{}, crd.ManifestGroup{}, nil)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		err := test.gwclient.MigrateHostnames(ctx, []string{"foobar.com"}, dseq, gseq)
		require.Error(t, err)
		require.IsType(t, apclient.ClientResponseError{}, err)
		require.Regexp(t, `(?s)^.*destination deployment does not exist.*$`, err.(apclient.ClientResponseError).ClientError())
	}

	runRouterTest(t, []routerTestAuth{routerTestAuthCert}, testFn)
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, testFn)
}

func TestRouteMigrateHostnameDeploymentDoesNotUse(t *testing.T) {
	testFn := func(test *routerTest, _ http.Header) {
		caddr := sdk.AccAddress(test.ckey.PubKey().Address())

		const dseq = uint64(133)
		const gseq = uint32(134)

		leaseID := testutil.LeaseID(t)

		leaseID.Owner = caddr.String()
		test.clusterService.On("FindActiveLease", mock.Anything, mock.Anything, dseq, gseq).Return(true, leaseID, crd.ManifestGroup{}, nil)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		err := test.gwclient.MigrateHostnames(ctx, []string{"foobar.org"}, dseq, gseq)
		require.Error(t, err)
		require.IsType(t, apclient.ClientResponseError{}, err)
		require.Regexp(t, `(?s)^.*the hostname "foobar.org" is not used by this deployment.*$`, err.(apclient.ClientResponseError).ClientError())
	}

	runRouterTest(t, []routerTestAuth{routerTestAuthCert}, testFn)
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, testFn)
}

func TestRouteMigrateHostname(t *testing.T) {
	const hostname = "kittens-purr.io"
	const dseq = uint64(133)
	const gseq = uint32(134)
	const serviceName = "hostly-service"
	const serviceExternalPort = uint32(1111)

	testFn := func(test *routerTest, _ http.Header) {
		mgroup := crd.ManifestGroup{
			Name: "some-group",
			Services: []crd.ManifestService{
				{
					Name:  serviceName,
					Image: "some-awesome-image",
					Count: 1,
					Expose: []crd.ManifestServiceExpose{
						{
							Port:         1234,
							ExternalPort: uint16(serviceExternalPort),
							Proto:        "TCP",
							Service:      serviceName,
							Global:       true,
							Hosts:        []string{"dogs.pet", hostname},
							/* Remaining fields not relevant in this test */
						},
					},
				},
			},
		}

		caddr := sdk.AccAddress(test.ckey.PubKey().Address())

		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()
		test.clusterService.On("FindActiveLease", mock.Anything, mock.Anything, dseq, gseq).Return(true, leaseID, mgroup, nil)
		test.hostnameClient.On("PrepareHostnamesForTransfer", mock.Anything, []string{hostname}, leaseID).Return(nil)
		test.clusterService.On("TransferHostname", mock.Anything, leaseID, hostname, serviceName, serviceExternalPort).Return(nil)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		err := test.gwclient.MigrateHostnames(ctx, []string{hostname}, dseq, gseq)
		require.NoError(t, err)

		require.Equal(t, 2, len(test.clusterService.Calls))
		require.Equal(t, "TransferHostname", test.clusterService.Calls[1].Method)
	}

	runRouterTest(t, []routerTestAuth{routerTestAuthCert}, testFn)
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, testFn)
}

func TestRouteMigrateHostnamePrepareFails(t *testing.T) {
	const hostname = "alphabet-soup.io"
	const dseq = uint64(7133)
	const gseq = uint32(7134)
	const serviceName = "hostly-service"
	const serviceExternalPort = uint32(999)

	testFn := func(test *routerTest, _ http.Header) {
		mgroup := crd.ManifestGroup{
			Name: "some-group",
			Services: []crd.ManifestService{
				{
					Name:  serviceName,
					Image: "some-awesome-image",
					Count: 1,
					Expose: []crd.ManifestServiceExpose{
						{
							Port:         1234,
							ExternalPort: uint16(serviceExternalPort),
							Proto:        "TCP",
							Service:      serviceName,
							Global:       true,
							Hosts:        []string{"dogs.pet", hostname},
							/* Remaining fields not relevant in this test */
						},
					},
				},
			},
		}

		caddr := sdk.AccAddress(test.ckey.PubKey().Address())

		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()
		test.clusterService.On("FindActiveLease", mock.Anything, mock.Anything, dseq, gseq).Return(true, leaseID, mgroup, nil)
		test.hostnameClient.On("PrepareHostnamesForTransfer", mock.Anything, []string{hostname}, leaseID).Return(io.EOF)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		err := test.gwclient.MigrateHostnames(ctx, []string{hostname}, dseq, gseq)
		require.Error(t, err)
		require.Regexp(t, `^.*remote server returned 500.*$`, err)
		require.Equal(t, 1, len(test.clusterService.Calls))
	}

	runRouterTest(t, []routerTestAuth{routerTestAuthCert}, testFn)
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, testFn)
}

func TestRouteMigrateHostnameTransferFails(t *testing.T) {
	const hostname = "moo.snake"
	const dseq = uint64(333)
	const gseq = uint32(334)
	const serviceName = "decloud-thing"
	const serviceExternalPort = uint32(1112)

	testFn := func(test *routerTest, _ http.Header) {

		mgroup := crd.ManifestGroup{
			Name: "some-group",
			Services: []crd.ManifestService{
				{
					Name:  serviceName,
					Image: "some-awesome-image",
					Count: 1,
					Expose: []crd.ManifestServiceExpose{
						{
							Port:         1234,
							ExternalPort: uint16(serviceExternalPort),
							Proto:        "TCP",
							Service:      serviceName,
							Global:       true,
							Hosts:        []string{"dogs.pet", hostname},
							/* Remaining fields not relevant in this test */
						},
					},
				},
			},
		}

		caddr := sdk.AccAddress(test.ckey.PubKey().Address())
		leaseID := testutil.LeaseID(t)
		leaseID.Owner = caddr.String()

		test.clusterService.On("FindActiveLease", mock.Anything, mock.Anything, dseq, gseq).Return(true, leaseID, mgroup, nil)
		test.hostnameClient.On("PrepareHostnamesForTransfer", mock.Anything, []string{hostname}, leaseID).Return(nil)
		test.clusterService.On("TransferHostname", mock.Anything, leaseID, hostname, serviceName, serviceExternalPort).Return(io.EOF)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
		defer cancel()
		err := test.gwclient.MigrateHostnames(ctx, []string{hostname}, dseq, gseq)
		require.Error(t, err)
		require.Regexp(t, `^.*remote server returned 500.*$`, err)
		require.Equal(t, 2, len(test.clusterService.Calls))
	}

	runRouterTest(t, []routerTestAuth{routerTestAuthCert}, testFn)
	runRouterTest(t, []routerTestAuth{routerTestAuthJWT}, testFn)
}
