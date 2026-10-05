package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	clientmocks "pkg.akt.dev/go/mocks/node/client"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	mv1 "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cmocks "github.com/akash-network/provider/mocks/cluster"
	clmocks "github.com/akash-network/provider/mocks/cluster/types"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/session"
)

type recoveryDeployProbe struct {
	inflight atomic.Int32
	peak     atomic.Int32
	admitted chan struct{}
	release  chan struct{}
}

func (p *recoveryDeployProbe) onDeploy(mock.Arguments) {
	n := p.inflight.Add(1)
	for {
		cur := p.peak.Load()
		if n <= cur || p.peak.CompareAndSwap(cur, n) {
			break
		}
	}
	p.admitted <- struct{}{}
	<-p.release
	p.inflight.Add(-1)
}

func newRecoveryTestDeploymentManager(t *testing.T, probe *recoveryDeployProbe, slots chan struct{}, shutdown <-chan struct{}) *deploymentManager {
	t.Helper()

	myLog := testutil.Logger(t)

	marketMocks := &marketmocks.QueryClient{}
	marketMocks.On("Lease", mock.Anything, mock.Anything).
		Return(&mvbeta.QueryLeaseResponse{Lease: mv1.Lease{State: mv1.LeaseActive}}, nil)

	queryMocks := &clientmocks.QueryClient{}
	queryMocks.On("Market").Return(marketMocks, nil)

	clientMocks := &clientmocks.Client{}
	clientMocks.On("Query").Return(queryMocks)

	mySession := session.New(myLog, clientMocks, nil, -1)

	client := &cmocks.Client{}
	client.On("GetDeclaredIPs", mock.Anything, mock.Anything).Return([]crd.ProviderLeasedIPSpec{}, nil)
	client.On("Deploy", mock.Anything, mock.Anything).Run(probe.onDeploy).Return(nil)

	hostnameMocks := &clmocks.HostnameServiceClient{}
	hostnameMocks.On("ReserveHostnames", mock.Anything, mock.Anything, mock.Anything).Return([]string{}, nil)

	return &deploymentManager{
		bus:     pubsub.NewBus(),
		client:  client,
		session: mySession,
		deployment: &ctypes.Deployment{
			Lid:    testutil.LeaseID(t),
			MGroup: &manifest.Group{},
		},
		log:                 myLog,
		hostnameService:     hostnameMocks,
		config:              NewDefaultConfig(),
		serviceShuttingDown: shutdown,
		recoveryDeploySlots: slots,
		currentHostnames:    make(map[string]struct{}),
	}
}

func recvAdmission(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testWait):
		t.Fatal("timed out waiting for deploy admission")
	}
}

func recvRunResult(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		require.NoError(t, err)
	case <-time.After(testWait):
		t.Fatal("timed out waiting for startDeploy result")
	}
}

func TestStartDeployBoundsRecoveryConcurrency(t *testing.T) {
	const n = 6
	const width = 2

	slots := make(chan struct{}, width)
	shutdown := make(chan struct{})
	probe := &recoveryDeployProbe{
		admitted: make(chan struct{}),
		release:  make(chan struct{}),
	}

	runchs := make([]<-chan error, n)
	for i := 0; i < n; i++ {
		dm := newRecoveryTestDeploymentManager(t, probe, slots, shutdown)
		runchs[i] = dm.startDeploy(context.Background(), dm.recoveryDeploySlots)
	}

	for i := 0; i < width; i++ {
		recvAdmission(t, probe.admitted)
	}

	close(probe.release)

	for i := width; i < n; i++ {
		recvAdmission(t, probe.admitted)
	}

	for _, ch := range runchs {
		recvRunResult(t, ch)
	}

	require.EqualValues(t, width, probe.peak.Load())
}

func TestStartDeployUngatedWhenSlotsNil(t *testing.T) {
	const n = 6

	shutdown := make(chan struct{})
	probe := &recoveryDeployProbe{
		admitted: make(chan struct{}),
		release:  make(chan struct{}),
	}

	runchs := make([]<-chan error, n)
	for i := 0; i < n; i++ {
		dm := newRecoveryTestDeploymentManager(t, probe, nil, shutdown)
		runchs[i] = dm.startDeploy(context.Background(), dm.recoveryDeploySlots)
	}

	for i := 0; i < n; i++ {
		recvAdmission(t, probe.admitted)
	}

	close(probe.release)

	for _, ch := range runchs {
		recvRunResult(t, ch)
	}

	require.EqualValues(t, n, probe.peak.Load())
}

func TestStartDeployAbortsQueuedRecoveryDeployOnShutdown(t *testing.T) {
	slots := make(chan struct{}, 1)
	shutdown := make(chan struct{})
	probe := &recoveryDeployProbe{
		admitted: make(chan struct{}),
		release:  make(chan struct{}),
	}

	holder := newRecoveryTestDeploymentManager(t, probe, slots, shutdown)
	holderRunch := holder.startDeploy(context.Background(), holder.recoveryDeploySlots)
	recvAdmission(t, probe.admitted)

	queued := newRecoveryTestDeploymentManager(t, probe, slots, shutdown)
	queuedRunch := queued.startDeploy(context.Background(), queued.recoveryDeploySlots)

	close(shutdown)

	select {
	case err := <-queuedRunch:
		require.ErrorIs(t, err, ErrNotRunning)
	case <-time.After(testWait):
		t.Fatal("queued deploy did not abort on shutdown")
	}

	close(probe.release)
	recvRunResult(t, holderRunch)

	require.EqualValues(t, 1, probe.peak.Load())
}
