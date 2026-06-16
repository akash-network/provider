package manifest

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"

	"github.com/akash-network/provider/utils/httperror"
)

// import (
// 	"context"
// 	"testing"
// 	"time"
//
// 	sdk "github.com/cosmos/cosmos-sdk/types"
// 	"github.com/stretchr/testify/mock"
// 	"github.com/stretchr/testify/require"
//
// 	dtypes "pkg.akt.dev/go/node/deployment/v1beta3"
// 	types "pkg.akt.dev/go/node/deployment/v1beta3"
// 	escrowtypes "pkg.akt.dev/go/node/escrow/v1beta3"
// 	mtypes "pkg.akt.dev/go/node/market/v1beta4"
// 	ptypes "pkg.akt.dev/go/node/provider/v1beta3"
// 	"pkg.akt.dev/go/sdkutil"
// 	clientMocks "pkg.akt.dev/node/v2/client/mocks"
// 	"pkg.akt.dev/go/util/pubsub"
// 	"pkg.akt.dev/go/sdl"
// 	"pkg.akt.dev/go/testutil"
//
// 	"github.com/akash-network/provider/cluster"
// 	clustertypes "github.com/akash-network/provider/cluster/types/v1beta3"
// 	"github.com/akash-network/provider/event"
// 	"github.com/akash-network/provider/session"
// )
//
// type scaffold struct {
// 	svc       *service
// 	cancel    context.CancelFunc
// 	bus       pubsub.Bus
// 	queryMock *clientMocks.QueryClient
// 	hostnames clustertypes.HostnameServiceClient
// }
//
// func serviceForManifestTest(t *testing.T, cfg ServiceConfig, mani sdl.SDL, did dtypes.DeploymentID, leases []mtypes.Lease, providerAddr string, delayQueryDeployment bool) *scaffold {
// 	clientMock := &clientMocks.Client{}
// 	queryMock := &clientMocks.QueryClient{}
//
// 	clientMock.On("Query").Return(queryMock)
//
// 	var version []byte
// 	var err error
// 	if mani != nil {
// 		m, err := mani.Manifest()
// 		require.NoError(t, err)
// 		version, err = m.Version()
// 		require.NoError(t, err)
// 		require.NotNil(t, version)
// 	} else {
// 		version = []byte("test")
// 	}
//
// 	var groups []dtypes.Group
// 	if mani != nil {
// 		dgroups, err := mani.DeploymentGroups()
// 		require.NoError(t, err)
// 		require.NotNil(t, dgroups)
// 		for i, g := range dgroups {
// 			groups = append(groups, dtypes.Group{
// 				GroupID: dtypes.GroupID{
// 					Owner: did.GetOwner(),
// 					DSeq:  did.DSeq,
// 					GSeq:  uint32(i),
// 				},
// 				State:     dtypes.GroupOpen,
// 				GroupSpec: *g,
// 			})
// 		}
// 	}
//
// 	res := &types.QueryDeploymentResponse{
// 		Deployment: types.Deployment{
// 			DeploymentID: did,
// 			State:        0,
// 			Version:      version,
// 		},
// 		Groups: groups,
// 	}
//
// 	x := queryMock.On("Deployment", mock.Anything, mock.Anything).After(time.Second*2).Return(res, nil)
// 	if delayQueryDeployment {
// 		x = x.After(time.Second * 2)
// 	}
// 	x.Return(res, nil)
//
// 	leasesMock := make([]mtypes.QueryLeaseResponse, 0)
// 	for _, lease := range leases {
// 		leasesMock = append(leasesMock, mtypes.QueryLeaseResponse{
// 			Lease: mtypes.Lease{
// 				LeaseID:   lease.GetLeaseID(),
// 				State:     lease.GetState(),
// 				Price:     lease.GetPrice(),
// 				CreatedAt: lease.GetCreatedAt(),
// 			},
// 			EscrowPayment: escrowtypes.FractionalPayment{}, // Ignored in this test
// 		})
// 	}
// 	queryMock.On("Leases", mock.Anything, &mtypes.QueryLeasesRequest{
// 		Filters: mtypes.LeaseFilters{
// 			Owner:    did.GetOwner(),
// 			DSeq:     did.GetDSeq(),
// 			GSeq:     0,
// 			OSeq:     0,
// 			Provider: providerAddr,
// 			State:    mtypes.LeaseActive.String(),
// 		},
// 		Pagination: nil,
// 	}).Return(&mtypes.QueryLeasesResponse{
// 		Leases:     leasesMock,
// 		Pagination: nil,
// 	}, nil)
//
// 	ctx, cancel := context.WithCancel(context.Background())
//
// 	// Use this type in test
// 	hostnames := cluster.NewSimpleHostnames()
//
// 	log := testutil.Logger(t)
// 	bus := pubsub.NewBus()
//
// 	p := &ptypes.Provider{
// 		Owner:      providerAddr,
// 		HostURI:    "",
// 		Attributes: nil,
// 	}
//
// 	createdAtBlockHeight := int64(-1)
// 	if len(leases) != 0 {
// 		createdAtBlockHeight = leases[0].GetCreatedAt() - 1
// 	}
// 	serviceInterface, err := NewService(ctx, session.New(log, clientMock, p, createdAtBlockHeight), bus, hostnames, cfg)
// 	require.NoError(t, err)
//
// 	svc := serviceInterface.(*service)
//
// 	return &scaffold{
// 		svc:       svc,
// 		cancel:    cancel,
// 		bus:       bus,
// 		queryMock: queryMock,
// 		hostnames: hostnames,
// 	}
// }
//
// func TestManagerReturnsWrongVersion(t *testing.T) {
// 	sdl2A, err := sdl.ReadFile("../testdata/deployment/deployment-v2-c2c.yaml")
// 	require.NoError(t, err)
//
// 	sdl2B, err := sdl.ReadFile("../testdata/deployment/deployment-v2.yaml")
// 	require.NoError(t, err)
//
// 	did := testutil.DeploymentID(t)
// 	s := serviceForManifestTest(t, ServiceConfig{}, sdl2B, did, nil, testutil.AccAddress(t).String(), false)
//
// 	sdlManifest, err := sdl2A.Manifest()
// 	require.NoError(t, err)
//
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.Error(t, err)
// 	require.ErrorIs(t, err, ErrManifestVersion)
//
// 	s.cancel()
//
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(10 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }
//
// func TestManagerReturnsNoLease(t *testing.T) {
// 	sdl2, err := sdl.ReadFile("../testdata/deployment/deployment-v2.yaml")
// 	require.NoError(t, err)
//
// 	did := testutil.DeploymentID(t)
// 	s := serviceForManifestTest(t, ServiceConfig{}, sdl2, did, nil, testutil.AccAddress(t).String(), false)
//
// 	sdlManifest, err := sdl2.Manifest()
// 	require.NoError(t, err)
//
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.Error(t, err)
// 	require.ErrorIs(t, err, ErrNoLeaseForDeployment)
//
// 	s.cancel()
//
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(10 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }
//
// func TestManagerHandlesTimeout(t *testing.T) {
// 	sdl2, err := sdl.ReadFile("../testdata/deployment/deployment-v2.yaml")
// 	require.NoError(t, err)
//
// 	sdlManifest, err := sdl2.Manifest()
// 	require.NoError(t, err)
//
// 	lid := testutil.LeaseID(t)
// 	lid.GSeq = 0
// 	did := lid.DeploymentID()
//
// 	dgroups, err := sdl2.DeploymentGroups()
// 	require.NoError(t, err)
//
// 	// Tell the service that a lease has been won
// 	dgroup := &dtypes.Group{
// 		GroupID:   lid.GroupID(),
// 		State:     0,
// 		GroupSpec: *dgroups[0],
// 	}
//
// 	ev := event.LeaseWon{
// 		LeaseID: lid,
// 		Group:   dgroup,
// 		Price: sdk.DecCoin{
// 			Denom:  testutil.CoinDenom,
// 			Amount: sdkmath.LegacyNewDec(111),
// 		},
// 	}
//
// 	s := serviceForManifestTest(t, ServiceConfig{HTTPServicesRequireAtLeastOneHost: true}, sdl2, did, nil, lid.GetProvider(), true)
// 	err = s.bus.Publish(ev)
// 	require.NoError(t, err)
// 	// time.Sleep(10 * time.Second) // Wait for publish to do its thing
//
// 	testctx, cancel := context.WithTimeout(context.Background(), time.Second)
// 	defer cancel()
// 	err = s.svc.Submit(testctx, did, sdlManifest)
// 	require.ErrorIs(t, err, context.DeadlineExceeded)
//
// 	s.cancel()
//
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(20 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }
//
// func TestManagerHandlesMissingGroup(t *testing.T) {
// 	sdl2, err := sdl.ReadFile("../testdata/deployment/deployment-v2.yaml")
// 	require.NoError(t, err)
//
// 	sdlManifest, err := sdl2.Manifest()
// 	require.NoError(t, err)
//
// 	lid := testutil.LeaseID(t)
// 	lid.GSeq = 99999
// 	did := lid.DeploymentID()
//
// 	version, err := sdlManifest.Version()
// 	require.NotNil(t, version)
// 	require.NoError(t, err)
// 	leases := []mtypes.Lease{{
// 		LeaseID: lid,
// 		State:   mtypes.LeaseActive,
// 		Price: sdk.DecCoin{
// 			Denom:  "uakt",
// 			Amount: sdkmath.LegacyNewDec(111),
// 		},
// 		CreatedAt: 0,
// 	}}
// 	s := serviceForManifestTest(t, ServiceConfig{}, sdl2, did, leases, lid.GetProvider(), false)
//
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.Error(t, err)
// 	require.Regexp(t, `^group not found:.+$`, err.Error())
//
// 	s.cancel()
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(10 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }
//
// func TestManagerRequiresHostname(t *testing.T) {
// 	sdl2, err := sdl.ReadFile("../testdata/deployment/deployment-v2-nohost.yaml")
// 	require.NoError(t, err)
//
// 	sdlManifest, err := sdl2.Manifest()
// 	require.NoError(t, err)
// 	require.Len(t, sdlManifest[0].Services[0].Expose[0].Hosts, 0)
//
// 	lid := testutil.LeaseID(t)
// 	lid.GSeq = 0
// 	did := lid.DeploymentID()
// 	dgroups, err := sdl2.DeploymentGroups()
// 	require.NoError(t, err)
//
// 	// Tell the service that a lease has been won
// 	dgroup := &dtypes.Group{
// 		GroupID:   lid.GroupID(),
// 		State:     0,
// 		GroupSpec: *dgroups[0],
// 	}
//
// 	ev := event.LeaseWon{
// 		LeaseID: lid,
// 		Group:   dgroup,
// 		Price:   sdk.NewDecCoin("uakt", sdkmath.NewInt(111)),
// 	}
// 	version, err := sdlManifest.Version()
// 	require.NotNil(t, version)
//
// 	require.NoError(t, err)
//
// 	leases := []mtypes.Lease{{
// 		LeaseID:   lid,
// 		State:     mtypes.LeaseActive,
// 		Price:     ev.Price,
// 		CreatedAt: 0,
// 	}}
// 	s := serviceForManifestTest(t, ServiceConfig{HTTPServicesRequireAtLeastOneHost: true}, sdl2, did, leases, lid.GetProvider(), false)
//
// 	err = s.bus.Publish(ev)
// 	require.NoError(t, err)
//
// 	time.Sleep(time.Second) // Wait for publish to do its thing
//
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.Error(t, err)
// 	require.Regexp(t, `^.+service ".+" exposed on .+:.+ must have a hostname$`, err.Error())
//
// 	s.cancel()
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(10 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }
//
// func TestManagerAllowsUpdate(t *testing.T) {
// 	sdl2, err := sdl.ReadFile("../testdata/deployment/deployment-v2.yaml")
// 	require.NoError(t, err)
// 	sdl2NewContainer, err := sdl.ReadFile("../testdata/deployment/deployment-v2-newcontainer.yaml")
// 	require.NoError(t, err)
//
// 	sdlManifest, err := sdl2.Manifest()
// 	require.NoError(t, err)
//
// 	lid := testutil.LeaseID(t)
// 	lid.GSeq = 0
// 	did := lid.DeploymentID()
// 	dgroups, err := sdl2.DeploymentGroups()
// 	require.NoError(t, err)
//
// 	// Tell the service that a lease has been won
// 	dgroup := &dtypes.Group{
// 		GroupID:   lid.GroupID(),
// 		State:     0,
// 		GroupSpec: *dgroups[0],
// 	}
//
// 	ev := event.LeaseWon{
// 		LeaseID: lid,
// 		Group:   dgroup,
// 		Price:   sdk.NewDecCoinFromDec(testutil.CoinDenom, sdkmath.LegacyNewDec(111)),
// 	}
// 	version, err := sdlManifest.Version()
// 	require.NotNil(t, version)
// 	require.NoError(t, err)
// 	leases := []mtypes.Lease{{
// 		LeaseID:   lid,
// 		State:     mtypes.LeaseActive,
// 		Price:     ev.Price,
// 		CreatedAt: 0,
// 	}}
// 	s := serviceForManifestTest(t, ServiceConfig{HTTPServicesRequireAtLeastOneHost: true}, sdl2, did, leases, lid.GetProvider(), false)
//
// 	err = s.bus.Publish(ev)
// 	require.NoError(t, err)
// 	time.Sleep(time.Second) // Wait for publish to do its thing
//
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.NoError(t, err)
//
// 	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
//
// 	// Pretend that the hostname has been reserved by a running deployment
// 	withheld, err := s.hostnames.ReserveHostnames(ctx, AllHostnamesOfManifestGroup(sdlManifest.GetGroups()[0]), lid)
// 	require.NoError(t, err)
// 	cancel()
// 	require.Len(t, withheld, 0)
//
// 	sdlManifest, err = sdl2NewContainer.Manifest()
// 	require.NoError(t, err)
//
// 	version, err = sdlManifestVersion()
// 	require.NoError(t, err)
//
// 	update := dtypes.EventDeploymentUpdated{
// 		Context: sdkutil.BaseModuleEvent{},
// 		ID:      did,
// 		Version: version,
// 	}
//
// 	err = s.bus.Publish(update)
// 	require.NoError(t, err)
// 	time.Sleep(time.Second) // Wait for publish to do its thing
//
// 	// Submit the new manifest
// 	err = s.svc.Submit(context.Background(), did, sdlManifest)
// 	require.NoError(t, err)
//
// 	s.cancel()
// 	select {
// 	case <-s.svc.lc.Done():
//
// 	case <-time.After(10 * time.Second):
// 		t.Fatal("timed out waiting for service shutdown")
// 	}
// }

// TestValidateRequestWrapsManifestError ensures that a manifest validation
// failure is tagged with ErrInvalidManifest, even when the underlying error
// from Manifest.Validate is not already tagged. This keeps the gateway from
// returning a 500 for what is a client-side error (see akash-network/support#421).
func TestValidateRequestWrapsManifestError(t *testing.T) {
	// A service with zero-value (nil) resources fails resource validation with a
	// raw error that upstream does not tag with ErrInvalidManifest.
	mani := maniv2beta2.Manifest{
		{
			Name: "group1",
			Services: []maniv2beta2.Service{
				{
					Name:  "svc1",
					Image: "test-image",
					Count: 1,
				},
			},
		},
	}

	rawErr := mani.Validate()
	require.Error(t, rawErr)
	require.NotErrorIs(t, rawErr, maniv2beta2.ErrInvalidManifest,
		"precondition: upstream error is expected to be untagged")

	m := &manager{}
	err := m.validateRequest(manifestRequest{
		ctx:   context.Background(),
		value: &submitRequest{Manifest: mani},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, maniv2beta2.ErrInvalidManifest)
	require.Equal(t, http.StatusUnprocessableEntity,
		httperror.StatusCodeFrom(ManifestSubmitErrorToHTTP(err)))
}
