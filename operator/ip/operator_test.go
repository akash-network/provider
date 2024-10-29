package ip

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	kubeErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/testutil"

	"github.com/akash-network/provider/cluster/mocks"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	clfromctx "github.com/akash-network/provider/cluster/types/v1beta3/fromctx"
	cutil "github.com/akash-network/provider/cluster/util"
	"github.com/akash-network/provider/operator/common"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	aclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/fromctx"

	mlbmocks "github.com/akash-network/provider/cluster/kube/operators/clients/metallb/mocks"
)

type ipOperatorScaffold struct {
	op          *ipOperator
	clusterMock *mocks.Client
	metalMock   *mlbmocks.MetalLBClient
	ilc         common.IgnoreListConfig
}

func runIPOperator(t *testing.T, run bool, aobj []runtime.Object, prerun, fn func(ctx context.Context, s ipOperatorScaffold)) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset(aobj...)

	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, clfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx))

	providerAddr := testutil.AccAddress(t)

	l := testutil.Logger(t)
	client := &mocks.Client{}
	mllbc := &mlbmocks.MetalLBClient{}
	mllbc.On("Stop")

	poolChangesMock := make(chan struct{})
	// nolint: golint, gosimple
	var poolChangesInterface <-chan struct{}
	poolChangesInterface = poolChangesMock
	mllbc.On("DetectPoolChanges", mock.Anything).Return(poolChangesInterface, nil)

	ilc := common.IgnoreListConfig{
		FailureLimit: 100,
		EntryLimit:   9999,
		AgeLimit:     time.Hour,
	}
	opcfg := common.OperatorConfig{
		PruneInterval:      time.Second,
		WebRefreshInterval: time.Second,
		RetryDelay:         time.Second,
		ProviderAddress:    providerAddr.String(),
	}
	op, err := newIPOperator(ctx, l, "lease", opcfg, ilc, mllbc)

	require.NoError(t, err)
	require.NotNil(t, op)

	s := ipOperatorScaffold{
		op:          op,
		metalMock:   mllbc,
		clusterMock: client,
		ilc:         ilc,
	}

	if run {
		if prerun != nil {
			prerun(ctx, s)
		}
		done := make(chan error)
		go func() {
			defer close(done)
			done <- op.run(ctx)
		}()

		fn(ctx, s)
		cancel()

		select {
		case err = <-done:
			require.Error(t, err)
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for ip operator to stop")
		}
	} else {
		fn(ctx, s)
	}
}

type fakeIPEvent struct {
	leaseID      mtypes.LeaseID
	externalPort uint32
	port         uint32
	sharingKey   string
	serviceName  string
	protocol     manifest.ServiceProtocol
	eventType    ctypes.ProviderResourceEvent
}

func (fipe fakeIPEvent) GetLeaseID() mtypes.LeaseID {
	return fipe.leaseID
}
func (fipe fakeIPEvent) GetExternalPort() uint32 {
	return fipe.externalPort
}
func (fipe fakeIPEvent) GetPort() uint32 {
	return fipe.port
}

func (fipe fakeIPEvent) GetSharingKey() string {
	return fipe.sharingKey
}

func (fipe fakeIPEvent) GetServiceName() string {
	return fipe.serviceName
}

func (fipe fakeIPEvent) GetProtocol() manifest.ServiceProtocol {
	return fipe.protocol
}

func (fipe fakeIPEvent) GetEventType() ctypes.ProviderResourceEvent {
	return fipe.eventType
}

func TestIPOperatorAddEvent(t *testing.T) {
	runIPOperator(t, false, []runtime.Object{}, nil, func(ctx context.Context, s ipOperatorScaffold) {
		require.NotNil(t, s.op)
		leaseID := testutil.LeaseID(t)

		s.metalMock.On("CreateIPPassthrough", mock.Anything,
			cip.ClusterIPPassthroughDirective{
				LeaseID:      leaseID,
				ServiceName:  "aservice",
				Port:         10000,
				ExternalPort: 10001,
				SharingKey:   "akey",
				Protocol:     "TCP",
			}).Return(nil)

		err := s.op.applyEvent(ctx, fakeIPEvent{
			leaseID:      leaseID,
			externalPort: 10001,
			port:         10000,
			sharingKey:   "akey",
			serviceName:  "aservice",
			protocol:     manifest.TCP,
			eventType:    ctypes.ProviderResourceAdd,
		})
		require.NoError(t, err)
	})
}

// Add for updating to a different lease
func TestIPOperatorUpdateEvent(t *testing.T) {
	runIPOperator(t, false, []runtime.Object{}, nil, func(ctx context.Context, s ipOperatorScaffold) {
		require.NotNil(t, s.op)
		leaseID := testutil.LeaseID(t)

		s.metalMock.On("CreateIPPassthrough", mock.Anything,
			cip.ClusterIPPassthroughDirective{
				LeaseID:      leaseID,
				ServiceName:  "aservice",
				Port:         10000,
				ExternalPort: 10001,
				SharingKey:   "akey",
				Protocol:     "TCP",
			}).Return(nil)

		err := s.op.applyEvent(ctx, fakeIPEvent{
			leaseID:      leaseID,
			externalPort: 10001,
			port:         10000,
			sharingKey:   "akey",
			serviceName:  "aservice",
			protocol:     manifest.TCP,
			eventType:    ctypes.ProviderResourceUpdate,
		})
		require.NoError(t, err)
	})
}

func TestIPOperatorDeleteEvent(t *testing.T) {
	runIPOperator(t, false, []runtime.Object{}, nil, func(ctx context.Context, s ipOperatorScaffold) {
		require.NotNil(t, s.op)
		leaseID := testutil.LeaseID(t)

		s.metalMock.On("PurgeIPPassthrough", mock.Anything,
			cip.ClusterIPPassthroughDirective{
				LeaseID:      leaseID,
				ServiceName:  "aservice",
				Port:         10000,
				ExternalPort: 10001,
				SharingKey:   "akey",
				Protocol:     "TCP",
			}).Return(nil)

		err := s.op.applyEvent(ctx, fakeIPEvent{
			leaseID:      leaseID,
			externalPort: 10001,
			port:         10000,
			sharingKey:   "akey",
			serviceName:  "aservice",
			protocol:     manifest.TCP,
			eventType:    ctypes.ProviderResourceDelete,
		})
		require.NoError(t, err)
	})
}

func TestIPOperatorGivesUpOnErrors(t *testing.T) {
	var fakeError = kubeErrors.NewNotFound(schema.GroupResource{
		Group:    "thegroup",
		Resource: "theresource",
	}, "bob")
	runIPOperator(t, false, []runtime.Object{}, nil, func(ctx context.Context, s ipOperatorScaffold) {
		require.NotNil(t, s.op)
		leaseID := testutil.LeaseID(t)

		s.metalMock.On("CreateIPPassthrough", mock.Anything,
			cip.ClusterIPPassthroughDirective{
				LeaseID:      leaseID,
				ServiceName:  "aservice",
				Port:         10000,
				ExternalPort: 10001,
				SharingKey:   "akey",
				Protocol:     "TCP",
			}).Return(fakeError).Times(int(s.ilc.FailureLimit)) // nolint: gosec

		require.Greater(t, s.ilc.FailureLimit, uint(0))

		fakeEvent := fakeIPEvent{
			leaseID:      leaseID,
			externalPort: 10001,
			port:         10000,
			sharingKey:   "akey",
			serviceName:  "aservice",
			protocol:     manifest.TCP,
			eventType:    ctypes.ProviderResourceAdd,
		}
		for i := uint(0); i != s.ilc.FailureLimit; i++ {
			err := s.op.applyEvent(ctx, fakeEvent)
			require.ErrorIs(t, err, fakeError)
		}

		err := s.op.applyEvent(ctx, fakeEvent)
		require.NoError(t, err) // Nothing happens because this is ignored
	})
}

func TestIPOperatorRun(t *testing.T) {
	leaseID := testutil.LeaseID(t)

	lip := &crd.ProviderLeasedIP{
		TypeMeta: metav1.TypeMeta{
			Kind:       "providerleasedips.akash.network",
			APIVersion: "v2beta2",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            cutil.LeaseIDToNamespace(leaseID),
			Namespace:       "lease",
			ResourceVersion: "test",
		},
		Spec: crd.ProviderLeasedIPSpec{
			LeaseID:      crd.LeaseIDFromAkash(leaseID),
			ServiceName:  "aservice",
			Port:         101,
			ExternalPort: 100,
			SharingKey:   "akey",
			Protocol:     manifest.UDP.ToString(),
		},
	}

	waitForEventRead := make(chan struct{}, 1)
	runIPOperator(t, true, []runtime.Object{lip}, func(ctx context.Context, s ipOperatorScaffold) {
		s.metalMock.On("GetIPPassthroughs", mock.Anything).Return(nil, nil)
		s.metalMock.On("GetIPAddressUsage", mock.Anything).Return(uint(0), uint(3), nil)
		events, err := s.op.observeIPState(ctx)
		require.NoError(t, err)
		go func() {
			<-events
			waitForEventRead <- struct{}{}
		}()

		s.metalMock.On("CreateIPPassthrough", mock.Anything,
			cip.ClusterIPPassthroughDirective{
				LeaseID:      leaseID,
				ServiceName:  "aservice",
				Port:         101,
				ExternalPort: 100,
				SharingKey:   "akey",
				Protocol:     manifest.UDP,
			}).Return(nil)

	}, func(ctx context.Context, s ipOperatorScaffold) {
		require.NotNil(t, s.op)

		select {
		case <-waitForEventRead:
		case <-ctx.Done():
			t.Fatalf("timeout waiting for event read")
		}
	})
}
