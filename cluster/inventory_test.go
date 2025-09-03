package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tpubsub "github.com/troian/pubsub"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/akash-api/go/node/types/unit"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/testutil"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	cinventory "github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	cipmocks "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip/mocks"
	cfromctx "github.com/akash-network/provider/cluster/types/v1beta3/fromctx"
	cmocks "github.com/akash-network/provider/cluster/types/v1beta3/mocks"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/operator/waiter"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	aclient "github.com/akash-network/provider/pkg/client/clientset/versioned"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/fromctx"
)

func TestInventory_reservationAllocatable(t *testing.T) {
	mkrg := func(cpu uint64, gpu uint64, memory uint64, storage uint64, endpointsCount uint, count uint32) dtypes.ResourceUnit {
		endpoints := make([]types.Endpoint, endpointsCount)
		return dtypes.ResourceUnit{
			Resources: types.Resources{
				ID: 1,
				CPU: &types.CPU{
					Units: types.NewResourceValue(cpu),
				},
				GPU: &types.GPU{
					Units: types.NewResourceValue(gpu),
				},
				Memory: &types.Memory{
					Quantity: types.NewResourceValue(memory),
				},
				Storage: []types.Storage{
					{
						Quantity: types.NewResourceValue(storage),
					},
				},
				Endpoints: endpoints,
			},
			Count: count,
		}
	}

	mkres := func(allocated bool, res ...dtypes.ResourceUnit) *reservation {
		return &reservation{
			allocated: allocated,
			resources: &dtypes.GroupSpec{Resources: res},
		}
	}

	inv := <-cinventory.NewNull(context.Background(), "a", "b").ResultChan()

	reservations := []*reservation{
		mkres(true, mkrg(750, 0, 3*unit.Gi, 1*unit.Gi, 0, 1)),
		mkres(true, mkrg(100, 0, 4*unit.Gi, 1*unit.Gi, 0, 2)),
		mkres(true, mkrg(2000, 0, 3*unit.Gi, 1*unit.Gi, 0, 2)),
		mkres(true, mkrg(250, 0, 12*unit.Gi, 1*unit.Gi, 0, 2)),
		mkres(true, mkrg(100, 0, 1*unit.G, 1*unit.Gi, 1, 2)),
		mkres(true, mkrg(100, 0, 4*unit.G, 1*unit.Gi, 0, 1)),
		mkres(true, mkrg(100, 0, 4*unit.G, 98*unit.Gi, 0, 1)),
		mkres(true, mkrg(250, 0, 1*unit.G, 1*unit.Gi, 0, 1)),
	}

	for idx, r := range reservations {
		err := inv.Adjust(r)
		require.NoErrorf(t, err, "reservation %d: %v", idx, r)
	}
}

func TestInventory_ClusterDeploymentNotDeployed(t *testing.T) {
	config := Config{
		InventoryResourcePollPeriod:     time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()
	subscriber, err := bus.Subscribe()
	require.NoError(t, err)

	deployments := make([]ctypes.IDeployment, 0)

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx, "nodeA"))

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		nil,
		waiter.NewNullWaiter(), // Do not need to wait in test
		deployments)
	require.NoError(t, err)
	require.NotNil(t, inv)

	cancel()
	<-inv.lc.Done()

	// No ports used yet
	require.Equal(t, uint(1000), inv.availableExternalPorts)
}

func TestInventory_ClusterDeploymentDeployed(t *testing.T) {
	lid := testutil.LeaseID(t)
	config := Config{
		InventoryResourcePollPeriod:     time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}
	myLog := testutil.Logger(t)
	bus := pubsub.NewBus()
	subscriber, err := bus.Subscribe()
	require.NoError(t, err)

	deployments := make([]ctypes.IDeployment, 1)
	deployment := &cmocks.IDeployment{}
	deployment.On("LeaseID").Return(lid)

	groupServices := make(manifest.Services, 1)

	serviceCount := testutil.RandRangeInt(1, 10)
	serviceEndpoints := make([]types.Endpoint, serviceCount)

	countOfRandomPortService := testutil.RandRangeInt(0, serviceCount)
	for i := range serviceEndpoints {
		if i < countOfRandomPortService {
			serviceEndpoints[i].Kind = types.Endpoint_RANDOM_PORT
		} else {
			serviceEndpoints[i].Kind = types.Endpoint_SHARED_HTTP
		}
	}

	groupServices[0] = manifest.Service{
		Count: 1,
		Resources: types.Resources{
			ID: 1,
			CPU: &types.CPU{
				Units: types.NewResourceValue(1),
			},
			GPU: &types.GPU{
				Units: types.NewResourceValue(0),
			},
			Memory: &types.Memory{
				Quantity: types.NewResourceValue(1 * unit.Gi),
			},
			Storage: []types.Storage{
				{
					Name:     "default",
					Quantity: types.NewResourceValue(1 * unit.Gi),
				},
			},
			Endpoints: serviceEndpoints,
		},
	}
	group := manifest.Group{
		Name:     "nameForGroup",
		Services: groupServices,
	}

	deployment.On("ManifestGroup").Return(&group)
	deployment.On("ClusterParams").Return(crd.ClusterSettings{})

	deployments[0] = deployment

	// clusterInv := newInventory("nodeA")
	//
	// inventoryCalled := make(chan int, 1)
	// clusterClient.On("Inventory", mock.Anything).Run(func(args mock.Arguments) {
	// 	inventoryCalled <- 0 // Value does not matter
	// }).Return(clusterInv, nil)

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx, "nodeA"))

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		nil,
		waiter.NewNullWaiter(), // Do not need to wait in test
		deployments)
	require.NoError(t, err)
	require.NotNil(t, inv)

	// Wait for first call to inventory
	// <-inventoryCalled

	// Send the event immediately, twice
	// Second version does nothing
	err = bus.Publish(event.ClusterDeployment{
		LeaseID: lid,
		Group: &manifest.Group{
			Name:     "nameForGroup",
			Services: nil,
		},
		Status: event.ClusterDeploymentDeployed,
	})
	require.NoError(t, err)

	err = bus.Publish(event.ClusterDeployment{
		LeaseID: lid,
		Group: &manifest.Group{
			Name:     "nameForGroup",
			Services: nil,
		},
		Status: event.ClusterDeploymentDeployed,
	})
	require.NoError(t, err)

	// Wait for second call to inventory
	// <-inventoryCalled

	// wait for cluster deployment to be active
	// needed to avoid data race in reading availableExternalPorts
	for {
		status, err := inv.status(context.Background())
		require.NoError(t, err)

		if len(status.Active) != 0 {
			break
		}

		time.Sleep(time.Second / 2)
	}

	// availableExternalEndpoints should be consumed because of the deployed reservation
	require.Equal(t, uint(1000-countOfRandomPortService), inv.availableExternalPorts) // nolint: gosec

	// Unreserving the allocated reservation should reclaim the availableExternalEndpoints
	err = inv.unreserve(lid.OrderID())
	require.NoError(t, err)
	require.Equal(t, uint(1000), inv.availableExternalPorts)

	// Shut everything down
	cancel()
	<-inv.lc.Done()
}

type inventoryScaffold struct {
	leaseIDs []mtypes.LeaseID
	donech   chan struct{}
	// inventoryCalled chan struct{}
	bus           pubsub.Bus
	clusterClient Client
}

func makeInventoryScaffold(t *testing.T, leaseQty uint) *inventoryScaffold {
	scaffold := &inventoryScaffold{
		donech: make(chan struct{}),
	}

	for i := uint(0); i != leaseQty; i++ {
		scaffold.leaseIDs = append(scaffold.leaseIDs, testutil.LeaseID(t))
	}

	scaffold.bus = pubsub.NewBus()

	groupServices := make([]manifest.Service, 1)
	serviceCount := testutil.RandRangeInt(1, 50)
	serviceEndpoints := make([]types.Endpoint, serviceCount)

	countOfRandomPortService := testutil.RandRangeInt(0, serviceCount)
	for i := range serviceEndpoints {
		if i < countOfRandomPortService {
			serviceEndpoints[i].Kind = types.Endpoint_RANDOM_PORT
		} else {
			serviceEndpoints[i].Kind = types.Endpoint_SHARED_HTTP
		}
	}

	deploymentRequirements := types.Resources{
		ID: 1,
		CPU: &types.CPU{
			Units: types.NewResourceValue(4000),
		},
		GPU: &types.GPU{
			Units: types.NewResourceValue(0),
		},
		Memory: &types.Memory{
			Quantity: types.NewResourceValue(30 * unit.Gi),
		},
		Storage: types.Volumes{
			types.Storage{
				Name:     "default",
				Quantity: types.NewResourceValue((100 * unit.Gi) - 1*unit.Mi),
			},
		},
	}

	deploymentRequirements.Endpoints = serviceEndpoints

	groupServices[0] = manifest.Service{
		Count:     1,
		Resources: deploymentRequirements,
	}

	scaffold.clusterClient = nil

	return scaffold
}

func makeGroupForInventoryTest(sharedHTTP, nodePort, leasedIP bool) manifest.Group {
	groupServices := make([]manifest.Service, 1)

	serviceEndpoints := make([]types.Endpoint, 0)
	seqno := uint32(0)
	if sharedHTTP {
		serviceEndpoint := types.Endpoint{
			Kind:           types.Endpoint_SHARED_HTTP,
			SequenceNumber: seqno,
		}
		serviceEndpoints = append(serviceEndpoints, serviceEndpoint)
	}

	if nodePort {
		serviceEndpoint := types.Endpoint{
			Kind:           types.Endpoint_RANDOM_PORT,
			SequenceNumber: seqno,
		}
		serviceEndpoints = append(serviceEndpoints, serviceEndpoint)
	}

	if leasedIP {
		serviceEndpoint := types.Endpoint{
			Kind:           types.Endpoint_LEASED_IP,
			SequenceNumber: seqno,
		}
		serviceEndpoints = append(serviceEndpoints, serviceEndpoint)
	}

	deploymentRequirements := types.Resources{
		ID: 1,
		CPU: &types.CPU{
			Units: types.NewResourceValue(4000),
		},
		GPU: &types.GPU{
			Units: types.NewResourceValue(0),
		},
		Memory: &types.Memory{
			Quantity: types.NewResourceValue(30 * unit.Gi),
		},
		Storage: types.Volumes{
			types.Storage{
				Name:     "default",
				Quantity: types.NewResourceValue((100 * unit.Gi) - 1*unit.Mi),
			},
		},
	}
	deploymentRequirements.Endpoints = serviceEndpoints

	groupServices[0] = manifest.Service{
		Count:     1,
		Resources: deploymentRequirements,
	}
	group := manifest.Group{
		Name:     "nameForGroup",
		Services: groupServices,
	}

	return group
}

func TestInventory_ReserveIPNoIPOperator(t *testing.T) {
	config := Config{
		InventoryResourcePollPeriod:     5 * time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}
	scaffold := makeInventoryScaffold(t, 10)
	defer scaffold.bus.Close()

	myLog := testutil.Logger(t)

	subscriber, err := scaffold.bus.Subscribe()
	require.NoError(t, err)

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx, "nodeA"))

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		scaffold.clusterClient,
		waiter.NewNullWaiter(), // Do not need to wait in test
		make([]ctypes.IDeployment, 0))
	require.NoError(t, err)
	require.NotNil(t, inv)

	group := makeGroupForInventoryTest(false, false, true)
	reservation, err := inv.reserve(scaffold.leaseIDs[0].OrderID(), group)
	require.ErrorIs(t, err, errNoLeasedIPsAvailable)
	require.Nil(t, reservation)

	// Shut everything down
	cancel()
	close(scaffold.donech)
	<-inv.lc.Done()
}

func TestInventory_ReserveIPUnavailableWithIPOperator(t *testing.T) {
	config := Config{
		InventoryResourcePollPeriod:     5 * time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}
	scaffold := makeInventoryScaffold(t, 10)
	defer scaffold.bus.Close()

	myLog := testutil.Logger(t)

	subscriber, err := scaffold.bus.Subscribe()
	require.NoError(t, err)

	mockIP := &cipmocks.Client{}

	ipQty := testutil.RandRangeInt(1, 100)
	mockIP.On("GetIPAddressUsage", mock.Anything).Return(cip.AddressUsage{
		Available: uint(ipQty), // nolint: gosec
		InUse:     uint(ipQty), // nolint: gosec
	}, nil)
	mockIP.On("Stop")

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx, "nodeA"))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientIP, cip.Client(mockIP))

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		scaffold.clusterClient,
		waiter.NewNullWaiter(), // Do not need to wait in test
		make([]ctypes.IDeployment, 0))
	require.NoError(t, err)
	require.NotNil(t, inv)

	group := makeGroupForInventoryTest(false, false, true)
	reservation, err := inv.reserve(scaffold.leaseIDs[0].OrderID(), group)
	require.ErrorIs(t, err, errInsufficientIPs)
	require.Nil(t, reservation)

	// Shut everything down
	cancel()
	close(scaffold.donech)
	<-inv.lc.Done()
}

func TestInventory_ReserveIPAvailableWithIPOperator(t *testing.T) {
	config := Config{
		InventoryResourcePollPeriod:     4 * time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}

	scaffold := makeInventoryScaffold(t, 2)
	defer scaffold.bus.Close()

	myLog := testutil.Logger(t)

	subscriber, err := scaffold.bus.Subscribe()
	require.NoError(t, err)

	mockIP := &cipmocks.Client{}

	ipQty := testutil.RandRangeInt(5, 10)
	mockIP.On("GetIPAddressUsage", mock.Anything).Return(cip.AddressUsage{
		Available: uint(ipQty),     // nolint: gosec
		InUse:     uint(ipQty - 1), // nolint: gosec
	}, nil)

	ipAddrStatusCalled := make(chan struct{}, 2)
	// First call indicates no data
	mockIP.On("GetIPAddressStatus", mock.Anything, scaffold.leaseIDs[0].OrderID()).Run(func(_ mock.Arguments) {
		ipAddrStatusCalled <- struct{}{}
	}).Return([]cip.LeaseIPStatus{}, nil).Once()
	// Second call indicates the IP is there and can be confirmed
	mockIP.On("GetIPAddressStatus", mock.Anything, scaffold.leaseIDs[0].OrderID()).Run(func(_ mock.Arguments) {
		ipAddrStatusCalled <- struct{}{}
	}).Return([]cip.LeaseIPStatus{
		{
			Port:         1234,
			ExternalPort: 1234,
			ServiceName:  "foobar",
			IP:           "24.1.2.3",
			Protocol:     "TCP",
		},
	}, nil).Once()

	mockIP.On("Stop")

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, cinventory.NewNull(ctx, "nodeA", "nodeB"))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientIP, cip.Client(mockIP))

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		scaffold.clusterClient,
		waiter.NewNullWaiter(), // Do not need to wait in test
		make([]ctypes.IDeployment, 0))
	require.NoError(t, err)
	require.NotNil(t, inv)

	group := makeGroupForInventoryTest(false, false, true)
	reservation, err := inv.reserve(scaffold.leaseIDs[0].OrderID(), group)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.False(t, reservation.Allocated())

	// next reservation fails
	reservation, err = inv.reserve(scaffold.leaseIDs[1].OrderID(), group)
	require.ErrorIs(t, err, errInsufficientIPs)
	require.Nil(t, reservation)

	err = scaffold.bus.Publish(event.ClusterDeployment{
		LeaseID: scaffold.leaseIDs[0],
		Group:   &group,
		Status:  event.ClusterDeploymentDeployed,
	})
	require.NoError(t, err)

	testutil.ChannelWaitForValueUpTo(t, ipAddrStatusCalled, 30*time.Second)
	testutil.ChannelWaitForValueUpTo(t, ipAddrStatusCalled, 30*time.Second)

	// with the 1st reservation confirmed, this one passes now
	reservation, err = inv.reserve(scaffold.leaseIDs[1].OrderID(), group)
	require.NoError(t, err)
	require.NotNil(t, reservation)

	// Shut everything down
	cancel()
	close(scaffold.donech)
	<-inv.lc.Done()

	mockIP.AssertNumberOfCalls(t, "GetIPAddressStatus", 2)
}

// following test needs refactoring it reports incorrect inventory
func TestInventory_OverReservations(t *testing.T) {
	scaffold := makeInventoryScaffold(t, 10)
	defer scaffold.bus.Close()
	lid0 := scaffold.leaseIDs[0]
	lid1 := scaffold.leaseIDs[1]
	myLog := testutil.Logger(t)

	subscriber, err := scaffold.bus.Subscribe()
	require.NoError(t, err)
	defer subscriber.Close()

	groupServices := make([]manifest.Service, 1)

	serviceCount := testutil.RandRangeInt(1, 50)
	serviceEndpoints := make([]types.Endpoint, serviceCount)

	countOfRandomPortService := testutil.RandRangeInt(0, serviceCount)
	for i := range serviceEndpoints {
		if i < countOfRandomPortService {
			serviceEndpoints[i].Kind = types.Endpoint_RANDOM_PORT
		} else {
			serviceEndpoints[i].Kind = types.Endpoint_SHARED_HTTP
		}
	}

	deploymentRequirements := types.Resources{
		CPU: &types.CPU{
			Units: types.NewResourceValue(4000),
		},
		GPU: &types.GPU{
			Units: types.NewResourceValue(0),
		},
		Memory: &types.Memory{
			Quantity: types.NewResourceValue(30 * unit.Gi),
		},
		Storage: types.Volumes{
			types.Storage{
				Name:     "default",
				Quantity: types.NewResourceValue((100 * unit.Gi) - 1*unit.Mi),
			},
		},
	}
	deploymentRequirements.Endpoints = serviceEndpoints

	groupServices[0] = manifest.Service{
		Count:     1,
		Resources: deploymentRequirements,
	}
	group := manifest.Group{
		Name:     "nameForGroup",
		Services: groupServices,
	}

	config := Config{
		InventoryResourcePollPeriod:     5 * time.Second,
		InventoryResourceDebugFrequency: 1,
		InventoryExternalPortQuantity:   1000,
	}

	kc := kfake.NewSimpleClientset()
	ac := afake.NewSimpleClientset()

	ctx, cancel := context.WithCancel(context.Background())
	nullInv := cinventory.NewNull(ctx, "nodeA")

	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, aclient.Interface(ac))
	ctx = context.WithValue(ctx, cfromctx.CtxKeyClientInventory, nullInv)

	inv, err := newInventoryService(
		ctx,
		config,
		myLog,
		subscriber,
		scaffold.clusterClient,
		waiter.NewNullWaiter(), // Do not need to wait in test
		make([]ctypes.IDeployment, 0))
	require.NoError(t, err)
	require.NotNil(t, inv)

	// Get the reservation
	reservation, err := inv.reserve(lid0.OrderID(), group)
	require.NoError(t, err)
	require.NotNil(t, reservation)

	// Confirm the second reservation would be too much
	_, err = inv.reserve(lid1.OrderID(), group)
	require.Error(t, err)
	require.ErrorIs(t, err, ctypes.ErrInsufficientCapacity)

	nullInv.Commit(reservation.Resources())

	// Send the event immediately to indicate it was deployed
	err = scaffold.bus.Publish(event.ClusterDeployment{
		LeaseID: lid0,
		Group: &manifest.Group{
			Name:     "nameForGroup",
			Services: nil,
		},
		Status: event.ClusterDeploymentDeployed,
	})
	require.NoError(t, err)

	// Give the inventory goroutine time to process the event
	time.Sleep(1 * time.Second)

	// Confirm the second reservation still is too much
	_, err = inv.reserve(lid1.OrderID(), group)
	require.ErrorIs(t, err, ctypes.ErrInsufficientCapacity)

	// Shut everything down
	cancel()
	close(scaffold.donech)
	<-inv.lc.Done()

	// No ports used yet
	require.Equal(t, uint(1000-countOfRandomPortService), inv.availableExternalPorts) // nolint: gosec
}
