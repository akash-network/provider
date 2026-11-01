package manifest

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	sdktypes "github.com/cosmos/cosmos-sdk/types"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"

	maniv2beta2 "pkg.akt.dev/go/manifest/v2beta3"
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	clustertypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/logging/logtest"
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

// TestManifestVersionFollowsChainReadsByHeight checks which manifest version the manager
// accepts after a sequence of chain reads and DeploymentUpdated events. The expected version
// is the deployment hash from the chain read at the greatest block height: a missed or late
// event, or a read from a node behind an earlier one, must not change it
// (see akash-network/support#662).
func TestManifestVersionFollowsChainReadsByHeight(t *testing.T) {
	fx := newVersionTestFixture(t)
	cur, prev := fx.currentHash, fx.previousHash

	read := func(hash []byte, height int64) func(*manager) {
		return func(m *manager) { m.applyFetchResult(fx.chainRead(hash, height)) }
	}
	update := func(hash []byte) func(*manager) {
		return func(m *manager) { m.applyUpdate(hash) }
	}

	tests := []struct {
		name  string
		steps []func(*manager)
		want  []byte // the version accepted; a manifest with the other version is refused
	}{
		{
			name:  "accepts the hash of the only chain read",
			steps: []func(*manager){read(prev, 10)},
			want:  prev,
		},
		{
			name:  "follows a newer chain read after a missed update event",
			steps: []func(*manager){update(prev), read(prev, 10), read(cur, 12)},
			want:  cur,
		},
		{
			name:  "ignores a chain read from a lower height",
			steps: []func(*manager){read(cur, 12), read(prev, 10)},
			want:  cur,
		},
		{
			name:  "ignores a late update event",
			steps: []func(*manager){read(cur, 12), update(prev), read(cur, 12)},
			want:  cur,
		},
		{
			name:  "ignores a late update event followed by a lagging chain read",
			steps: []func(*manager){read(cur, 12), update(prev), read(prev, 10)},
			want:  cur,
		},
		{
			name:  "follows a reverted hash read at a greater height",
			steps: []func(*manager){read(cur, 10), update(prev), read(prev, 12), read(cur, 14)},
			want:  cur,
		},
		{
			name:  "follows the latest chain read while no read reports a height",
			steps: []func(*manager){read(prev, 0), read(cur, 0)},
			want:  cur,
		},
		{
			name:  "ignores a chain read without a height once a read reported one",
			steps: []func(*manager){read(cur, 12), read(prev, 0)},
			want:  cur,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &manager{daddr: fx.did, log: testutil.Logger(t), hostnameService: allowAllHostnames{}}
			for _, step := range tt.steps {
				step(m)
			}

			accepted, refused := fx.current, fx.previous
			if bytes.Equal(tt.want, prev) {
				accepted, refused = fx.previous, fx.current
			}

			require.NoError(t, fx.validate(m, accepted))
			require.ErrorIs(t, fx.validate(m, refused), ErrManifestVersion)
		})
	}
}

// TestManagerChecksManifestsAgainstChainReads runs the manager against scripted deployment
// reads. A manifest is checked against a fresh read, and when it does not match, the
// manager reads the chain once more before refusing it, since the read may predate the
// update the manifest is for. Reads carry their block height, so a read from a node behind
// an earlier one does not change the expected version.
func TestManagerChecksManifestsAgainstChainReads(t *testing.T) {
	fx := newVersionTestFixture(t)
	cur, prev := fx.currentHash, fx.previousHash

	tests := []struct {
		name      string
		chain     []scriptedRead // deployment reads in order; the last one repeats
		results   []error        // of submitting the current manifest, once per entry
		wantReads int32
	}{
		{
			name:      "accepts a manifest the chain read matches",
			chain:     []scriptedRead{{hash: cur, height: 10}},
			results:   []error{nil},
			wantReads: 1,
		},
		{
			name:      "reads the chain again before refusing a manifest",
			chain:     []scriptedRead{{hash: prev, height: 10}, {hash: cur, height: 12}},
			results:   []error{nil},
			wantReads: 2,
		},
		{
			name:      "refuses a manifest the second read does not match either",
			chain:     []scriptedRead{{hash: prev, height: 10}, {hash: prev, height: 11}},
			results:   []error{ErrManifestVersion},
			wantReads: 2,
		},
		{
			name:      "keeps the version read at a greater height when a later read lags",
			chain:     []scriptedRead{{hash: cur, height: 12}, {hash: prev, height: 10}},
			results:   []error{nil, nil},
			wantReads: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := startVersionTestManager(t, fx, tt.chain, 0)

			for i, want := range tt.results {
				err := fx.submit(t, h.m, fx.current)
				if want == nil {
					require.NoError(t, err, "submission %d", i+1)
					continue
				}
				require.ErrorIs(t, err, want, "submission %d", i+1)
			}

			h.stop()
			require.Equal(t, tt.wantReads, h.deployments.calls.Load())
		})
	}
}

// TestManagerRereadsChainWhenCachedReadIsStale submits a manifest while the manager still
// holds a fresh cached read of the previous version, as when the update event for the new
// version is late or missed: the manager must read the chain again rather than refuse the
// manifest from its cache, and must not send the previous manifest to the cluster again
// while it does.
func TestManagerRereadsChainWhenCachedReadIsStale(t *testing.T) {
	fx := newVersionTestFixture(t)
	h := startVersionTestManager(t, fx, []scriptedRead{
		{hash: fx.previousHash, height: 10},
		{hash: fx.currentHash, height: 12},
	}, time.Hour)

	require.NoError(t, fx.submit(t, h.m, fx.previous))
	require.NoError(t, fx.submit(t, h.m, fx.current))

	h.stop()
	require.Equal(t, int32(2), h.deployments.calls.Load())
	require.Equal(t, [][]byte{fx.previousHash, fx.currentHash}, h.publishedVersions(t, 2))
}

// TestManagerAnswersManifestWhenRereadFails checks that a manifest kept for another chain
// read gets that read's error when the read fails, rather than being left waiting.
func TestManagerAnswersManifestWhenRereadFails(t *testing.T) {
	fx := newVersionTestFixture(t)
	errRead := errors.New("node unavailable")
	h := startVersionTestManager(t, fx, []scriptedRead{
		{hash: fx.previousHash, height: 10},
		{err: errRead},
	}, 0)

	require.ErrorIs(t, fx.submit(t, h.m, fx.current), errRead)

	h.stop()
	require.Equal(t, int32(2), h.deployments.calls.Load())
}

// TestManagerLogsChainReadBehindEarlierOne checks that an operator sees, at the default log
// level, why a chain read did not change the expected version.
func TestManagerLogsChainReadBehindEarlierOne(t *testing.T) {
	fx := newVersionTestFixture(t)
	rec := logtest.New()
	m := &manager{daddr: fx.did, log: rec, hostnameService: allowAllHostnames{}}

	m.applyFetchResult(fx.chainRead(fx.currentHash, 12))
	m.applyFetchResult(fx.chainRead(fx.previousHash, 10))

	entries := rec.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, "info", entries[0].Level)
	require.Equal(t, "chain read behind an earlier one, keeping expected version", entries[0].Msg)
	require.Equal(t, int64(10), entries[0].KV["height"])
	require.Equal(t, int64(12), entries[0].KV["expected_height"])
}

// TestManagerLogsVersionMismatch checks that the mismatch refusing a manifest is logged at
// error level with the versions compared, while a first mismatch, which only leads to
// another chain read, is logged at info.
func TestManagerLogsVersionMismatch(t *testing.T) {
	fx := newVersionTestFixture(t)
	rec := logtest.New()
	m := &manager{daddr: fx.did, log: rec, hostnameService: allowAllHostnames{}}
	m.applyFetchResult(fx.chainRead(fx.previousHash, 10))

	for _, reread := range []bool{false, true} {
		err := m.validateRequest(manifestRequest{
			ctx:    context.Background(),
			value:  &submitRequest{Deployment: fx.did, Manifest: fx.current},
			reread: reread,
		})
		require.ErrorIs(t, err, ErrManifestVersion)
	}

	entries := rec.Entries()
	require.Len(t, entries, 2)
	require.Equal(t, "info", entries[0].Level)
	require.Equal(t, "error", entries[1].Level)
	for _, entry := range entries {
		require.Equal(t, "deployment version mismatch", entry.Msg)
		require.Equal(t, hex.EncodeToString(fx.previousHash), entry.KV["expected"])
		require.Equal(t, int64(10), entry.KV["expected_height"])
		require.Equal(t, hex.EncodeToString(fx.currentHash), entry.KV["got"])
	}
}

func TestReadHeight(t *testing.T) {
	tests := []struct {
		name   string
		header metadata.MD
		want   int64
	}{
		{name: "reported", header: metadata.Pairs(grpctypes.GRPCBlockHeightHeader, "12"), want: 12},
		{name: "not reported", header: nil, want: 0},
		{name: "not a number", header: metadata.Pairs(grpctypes.GRPCBlockHeightHeader, "twelve"), want: 0},
		{name: "negative", header: metadata.Pairs(grpctypes.GRPCBlockHeightHeader, "-1"), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, readHeight(tt.header))
		})
	}
}

// versionTestFixture is a deployment and two manifests for it that differ only in version.
type versionTestFixture struct {
	did          dtypes.DeploymentID
	groups       dvbeta.Groups
	current      maniv2beta2.Manifest
	previous     maniv2beta2.Manifest
	currentHash  []byte
	previousHash []byte
}

func newVersionTestFixture(t *testing.T) versionTestFixture {
	t.Helper()

	fx := versionTestFixture{did: testutil.DeploymentID(t)}

	sdlObj, err := sdl.ReadFile("../testdata/sdl/simple.yaml")
	require.NoError(t, err)

	fx.current, err = sdlObj.Manifest()
	require.NoError(t, err)
	fx.currentHash, err = fx.current.Version()
	require.NoError(t, err)

	// The same SDL with another image: same groups, another version.
	prevObj, err := sdl.ReadFile("../testdata/sdl/simple.yaml")
	require.NoError(t, err)
	fx.previous, err = prevObj.Manifest()
	require.NoError(t, err)
	fx.previous[0].Services[0].Image = "nginx:1.25"
	fx.previousHash, err = fx.previous.Version()
	require.NoError(t, err)
	require.NotEqual(t, fx.currentHash, fx.previousHash)

	specs, err := sdlObj.DeploymentGroups()
	require.NoError(t, err)

	gseq := uint32(0)
	for _, spec := range specs {
		gseq++
		fx.groups = append(fx.groups, dvbeta.Group{
			ID:        dtypes.GroupID{Owner: fx.did.Owner, DSeq: fx.did.DSeq, GSeq: gseq},
			State:     dvbeta.GroupOpen,
			GroupSpec: spec,
		})
	}

	return fx
}

// chainRead is a fetch result whose deployment has hash, read at block height.
func (fx versionTestFixture) chainRead(hash []byte, height int64) manifestManagerFetchDataResult {
	return manifestManagerFetchDataResult{
		deployment: dvbeta.QueryDeploymentResponse{
			Deployment: dtypes.Deployment{ID: fx.did, Hash: hash},
			Groups:     fx.groups,
		},
		height: height,
	}
}

func (fx versionTestFixture) validate(m *manager, mani maniv2beta2.Manifest) error {
	return m.validateRequest(manifestRequest{
		ctx:   context.Background(),
		value: &submitRequest{Deployment: fx.did, Manifest: mani},
	})
}

// submit sends mani to m the way the manifest service does and waits for the answer.
func (fx versionTestFixture) submit(t *testing.T, m *manager, mani maniv2beta2.Manifest) error {
	t.Helper()

	ch := make(chan error, 1)
	m.handleManifest(manifestRequest{
		ctx:   context.Background(),
		value: &submitRequest{Deployment: fx.did, Manifest: mani},
		ch:    ch,
	})

	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		require.FailNow(t, "manifest was not answered")
		return nil
	}
}

// versionTestManager is a running manager with scripted chain queries, and a subscriber to
// the events it publishes.
type versionTestManager struct {
	m           *manager
	deployments *scriptedDeployments
	events      pubsub.Subscriber
	// stop stops the manager and waits for it, including any chain read it started.
	stop func()
}

// publishedVersions returns the versions of the next n manifests the manager published.
func (h *versionTestManager) publishedVersions(t *testing.T, n int) [][]byte {
	t.Helper()

	versions := make([][]byte, 0, n)
	for len(versions) < n {
		select {
		case ev := <-h.events.Events():
			received, ok := ev.(event.ManifestReceived)
			if !ok {
				continue
			}
			version, err := received.Manifest.Version()
			require.NoError(t, err)
			versions = append(versions, version)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "manifest was not published")
		}
	}

	return versions
}

// startVersionTestManager runs a manager for fx's deployment, with one active lease, against
// a chain that answers deployment queries with reads. The manager reuses a chain read for
// cacheAge; with 0, every manifest is checked against a new read.
func startVersionTestManager(
	t *testing.T,
	fx versionTestFixture,
	reads []scriptedRead,
	cacheAge time.Duration,
) *versionTestManager {
	t.Helper()

	provider := testutil.Provider(t)
	deployments := &scriptedDeployments{fx: fx, reads: reads}
	leases := activeLease{lease: mtypes.Lease{
		ID: mtypes.LeaseID{
			Owner:    fx.did.Owner,
			DSeq:     fx.did.DSeq,
			GSeq:     fx.groups[0].ID.GSeq,
			OSeq:     1,
			Provider: provider.Owner,
		},
		State: mtypes.LeaseActive,
	}}

	query := clientmocks.NewQueryClient(t)
	query.EXPECT().Deployment().Return(deployments)
	query.EXPECT().Market().Return(leases)
	client := clientmocks.NewClient(t)
	client.EXPECT().Query().Return(query)

	bus := pubsub.NewBus()
	t.Cleanup(bus.Close)

	events, err := bus.Subscribe()
	require.NoError(t, err)
	t.Cleanup(events.Close)

	svc := &service{
		session:         session.New(testutil.Logger(t), client, &provider, -1),
		bus:             bus,
		lc:              lifecycle.New(),
		managerch:       make(chan *manager, 1),
		hostnameService: allowAllHostnames{},
		config:          ServiceConfig{RPCQueryTimeout: time.Minute, CachedResultMaxAge: cacheAge},
	}

	m := newManager(svc, fx.did)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			m.stop()
			<-svc.managerch
		})
	}
	t.Cleanup(stop)

	return &versionTestManager{m: m, deployments: deployments, events: events, stop: stop}
}

// scriptedRead is a deployment hash and the block height a read returns it at, or the error
// the read fails with.
type scriptedRead struct {
	hash   []byte
	height int64
	err    error
}

// scriptedDeployments answers deployment queries with reads in order, reporting each read's
// block height in the response header as Cosmos SDK does. Once the reads are used up it
// keeps answering with the last one.
type scriptedDeployments struct {
	dvbeta.QueryClient

	fx    versionTestFixture
	reads []scriptedRead
	calls atomic.Int32
}

func (s *scriptedDeployments) Deployment(
	_ context.Context,
	_ *dvbeta.QueryDeploymentRequest,
	opts ...grpc.CallOption,
) (*dvbeta.QueryDeploymentResponse, error) {
	read := s.reads[min(int(s.calls.Add(1)), len(s.reads))-1]
	if read.err != nil {
		return nil, read.err
	}

	for _, opt := range opts {
		if header, ok := opt.(grpc.HeaderCallOption); ok {
			*header.HeaderAddr = metadata.Pairs(grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(read.height, 10))
		}
	}

	return &dvbeta.QueryDeploymentResponse{
		Deployment: dtypes.Deployment{ID: s.fx.did, Hash: read.hash},
		Groups:     s.fx.groups,
	}, nil
}

// activeLease answers lease queries with one lease.
type activeLease struct {
	mvbeta.QueryClient

	lease mtypes.Lease
}

func (l activeLease) Leases(context.Context, *mvbeta.QueryLeasesRequest, ...grpc.CallOption) (*mvbeta.QueryLeasesResponse, error) {
	return &mvbeta.QueryLeasesResponse{Leases: []mvbeta.QueryLeaseResponse{{Lease: l.lease}}}, nil
}

// allowAllHostnames is a hostname service with every hostname available.
type allowAllHostnames struct {
	clustertypes.HostnameServiceClient
}

func (allowAllHostnames) CanReserveHostnames([]string, sdktypes.Address) error {
	return nil
}
