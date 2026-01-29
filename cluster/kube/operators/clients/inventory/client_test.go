package inventory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/types/known/emptypb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	k8stest "k8s.io/client-go/testing"

	"pkg.akt.dev/go/grpc/gogoreflection"
	inventoryV1 "pkg.akt.dev/go/inventory/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/node/types/unit"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/testutil"
	"github.com/akash-network/provider/tools/fromctx"
)

type testReservation struct {
	resources         dvbeta.GroupSpec
	adjustedResources dvbeta.ResourceUnits
	cparams           interface{}
}

type testInventoryServer struct {
	inventoryV1.ClusterRPCServer
	ctx   context.Context
	invch chan inventoryV1.Cluster
}

var (
	testOperatorLabels = map[string]string{
		"app.kubernetes.io/name":      "inventory",
		"app.kubernetes.io/instance":  "inventory-service",
		"app.kubernetes.io/component": "operator",
		"app.kubernetes.io/part-of":   "provider",
	}
)

var _ ctypes.Reservation = (*testReservation)(nil)

func (r *testReservation) OrderID() mtypes.OrderID {
	return mtypes.OrderID{}
}

func (r *testReservation) Resources() dvbeta.ResourceGroup {
	return r.resources
}

func (r *testReservation) SetAllocatedResources(val dvbeta.ResourceUnits) {
	r.adjustedResources = val
}

func (r *testReservation) GetAllocatedResources() dvbeta.ResourceUnits {
	return r.adjustedResources
}

func (r *testReservation) Allocated() bool {
	return false
}

func (r *testReservation) SetClusterParams(val interface{}) {
	r.cparams = val
}

func (r *testReservation) ClusterParams() interface{} {
	return r.cparams
}

// type proxyCallback func(req *http.Request) (*http.Response, error)
type inventoryScaffold struct {
	ctx   context.Context
	group *errgroup.Group
	gInv  *testInventoryServer
	kc    *kfake.Clientset
	ports []int
}

func (sf *inventoryScaffold) createFakeOperator(t *testing.T) {
	t.Helper()

	const namespace = "akash-services"

	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator-inventory",
			Namespace: namespace,
			Labels:    testOperatorLabels,
		},
		Spec: corev1.ServiceSpec{
			Selector: testOperatorLabels,
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Protocol:   "tcp",
					Port:       int32(sf.ports[0]), // nolint: gosec
					TargetPort: intstr.FromString("grpc"),
				},
			},
		},
	}

	depl := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator-inventory",
			Namespace: namespace,
			Labels:    testOperatorLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: testOperatorLabels,
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "operator-inventory",
							Image: "test",
							Ports: []corev1.ContainerPort{
								{
									Name:          "grpc",
									ContainerPort: int32(sf.ports[0]), // nolint: gosec
								},
							},
						},
					},
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "operator-inventory-1",
			Namespace: namespace,
			Labels:    testOperatorLabels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "operator-inventory",
					Image: "test",
					Ports: []corev1.ContainerPort{
						{
							Name:          "grpc",
							ContainerPort: int32(sf.ports[0]), // nolint: gosec
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	_, err := sf.kc.CoreV1().Services(namespace).Create(context.TODO(), svc, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = sf.kc.AppsV1().Deployments(namespace).Create(context.TODO(), depl, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = sf.kc.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	require.NoError(t, err)

	watcher := watch.NewFake()
	sf.kc.PrependWatchReactor("pods", k8stest.DefaultWatchReactor(watcher, nil))
	sf.kc.PrependWatchReactor("deployments", k8stest.DefaultWatchReactor(watcher, nil))
	sf.kc.PrependWatchReactor("services", k8stest.DefaultWatchReactor(watcher, nil))

	go func() {
		watcher.Add(svc)
		watcher.Add(depl)
		watcher.Add(pod)
	}()
}

func makeInventoryScaffold(t *testing.T) *inventoryScaffold {
	t.Helper()

	ports, err := testutil.GetFreePorts(1)
	require.NoError(t, err)
	require.Len(t, ports, 1)

	group, ctx := errgroup.WithContext(context.Background())

	kc := kfake.NewClientset()
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
	ctx = context.WithValue(ctx, fromctx.CtxKeyInventoryUnderTest, true)

	gSrv := setupInventoryGRPC(ctx, group, ports[0])

	s := &inventoryScaffold{
		ctx:   ctx,
		group: group,
		gInv:  gSrv,
		kc:    kc,
		ports: ports,
	}

	s.createFakeOperator(t)

	return s
}

// QueryCluster does not need to be implemented as provider only uses stream
func (gm *testInventoryServer) QueryCluster(_ context.Context, _ *emptypb.Empty) (*inventoryV1.Cluster, error) {
	return nil, errors.New("unimplemented") // nolint: err113
}

func (gm *testInventoryServer) StreamCluster(_ *emptypb.Empty, stream inventoryV1.ClusterRPC_StreamClusterServer) error {
	for {
		select {
		case <-gm.ctx.Done():
			return gm.ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		case msg := <-gm.invch:
			if err := stream.Send(msg.Dup()); err != nil {
				return err
			}
		}
	}
}

func setupInventoryGRPC(ctx context.Context, group *errgroup.Group, port int) *testInventoryServer {
	grpcSrv := grpc.NewServer(grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
		MinTime:             30 * time.Second,
		PermitWithoutStream: false,
	}))

	gSrv := &testInventoryServer{
		ctx:   ctx,
		invch: make(chan inventoryV1.Cluster, 1),
	}

	inventoryV1.RegisterClusterRPCServer(grpcSrv, gSrv)
	gogoreflection.Register(grpcSrv)

	grpcEndpoint := fmt.Sprintf("localhost:%d", port)

	group.Go(func() error {
		grpcLis, err := net.Listen("tcp", grpcEndpoint)
		if err != nil {
			return err
		}

		return grpcSrv.Serve(grpcLis)
	})

	group.Go(func() error {
		<-ctx.Done()

		grpcSrv.GracefulStop()

		return ctx.Err()
	})

	return gSrv
}

func waitForInventory(t *testing.T, ch <-chan ctypes.Inventory) ctypes.Inventory {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	select {
	case res := <-ch:
		return res
	case <-ctx.Done():
		t.Error("timed out waiting for inventory")

		return nil
	}
}

func TestInventoryZero(t *testing.T) {
	scaffold := makeInventoryScaffold(t)

	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{}

	inv := waitForInventory(t, cl.ResultChan())

	require.NotNil(t, inv)

	// The inventory was called and the kubernetes client says there are no nodes & no pods. Inventory
	// should be zero
	require.Len(t, inv.Metrics().Nodes, 0)
}

func TestInventorySingleNodeNoPods(t *testing.T) {
	const expectedCPU = 13
	const expectedMemory = 14
	const expectedStorage = 15

	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: inventoryV1.Nodes{
			inventoryV1.Node{
				Name: "test",
				Resources: inventoryV1.NodeResources{
					CPU: inventoryV1.CPU{
						Quantity: inventoryV1.NewResourcePair(expectedCPU, expectedCPU, 0, "m"),
					},
					Memory: inventoryV1.Memory{
						Quantity: inventoryV1.NewResourcePair(expectedMemory, expectedMemory, 0, "M"),
					},
					GPU: inventoryV1.GPU{
						Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					},
					EphemeralStorage: inventoryV1.NewResourcePair(expectedStorage, expectedStorage, 0, "M"),
					VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				Capabilities: inventoryV1.NodeCapabilities{},
			},
		},
	}

	inv := waitForInventory(t, cl.ResultChan())

	require.Len(t, inv.Metrics().Nodes, 1)

	node := inv.Metrics().Nodes[0]

	availableResources := node.Available
	assert.Equal(t, uint64(expectedCPU*1000), availableResources.CPU)
	assert.Equal(t, uint64(expectedMemory), availableResources.Memory)
	assert.Equal(t, uint64(expectedStorage), availableResources.StorageEphemeral)
}

func TestInventorySingleNodeWithPods(t *testing.T) {
	const expectedCPU = 13
	const expectedMemory = 2048
	const expectedStorage = 4096

	const cpuPerContainer = 1
	const memoryPerContainer = 3
	const storagePerContainer = 17
	const totalContainers = 3

	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: inventoryV1.Nodes{
			inventoryV1.Node{
				Name: "test",
				Resources: inventoryV1.NodeResources{
					CPU: inventoryV1.CPU{
						Quantity: inventoryV1.NewResourcePair(expectedCPU, expectedCPU, cpuPerContainer*totalContainers, "m"),
					},
					Memory: inventoryV1.Memory{
						Quantity: inventoryV1.NewResourcePair(expectedMemory, expectedMemory, memoryPerContainer*totalContainers, "M"),
					},
					GPU: inventoryV1.GPU{
						Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					},
					EphemeralStorage: inventoryV1.NewResourcePair(expectedStorage, expectedStorage, storagePerContainer*totalContainers, "M"),
					VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				Capabilities: inventoryV1.NodeCapabilities{},
			},
		},
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)

	require.Len(t, inv.Metrics().Nodes, 1)

	node := inv.Metrics().Nodes[0]
	availableResources := node.Available
	// Multiply expected value by 1000 since millicpu is used
	assert.Equal(t, (uint64(expectedCPU)-(totalContainers*cpuPerContainer))*1000, availableResources.CPU)
	assert.Equal(t, uint64(expectedMemory)-totalContainers*memoryPerContainer, availableResources.Memory)
	assert.Equal(t, uint64(expectedStorage)-totalContainers*storagePerContainer, availableResources.StorageEphemeral)
}

func TestInventoryMultipleReplicasFulFilled1(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	reservation := multipleReplicasGenReservations(100000, 0, 2)
	err = inv.Adjust(reservation)
	require.NoError(t, err)
	require.NotNil(t, reservation.cparams)
	require.IsType(t, crd.ReservationClusterSettings{}, reservation.cparams)

	cparams := reservation.cparams.(crd.ReservationClusterSettings)
	require.Len(t, cparams, len(reservation.resources.Resources))
	sparams, exists := cparams[reservation.resources.Resources[0].ID]

	t.Logf("cparams: %v", cparams)

	require.True(t, exists)
	require.Nil(t, sparams)
}

func TestInventoryMultipleReplicasFulFilled2(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 4))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled3(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68800, 0, 3))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled4(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(119495, 0, 2))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled5(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled6(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled7(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleSvcReplicasGenReservations(68700, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasOutOfCapacity1(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(70000, 0, 4))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

func TestInventoryMultipleReplicasOutOfCapacity2(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(100000, 0, 3))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

func TestInventoryMultipleReplicasOutOfCapacity4(t *testing.T) {
	scaffold := makeInventoryScaffold(t)
	cl, err := NewClient(scaffold.ctx)
	require.NoError(t, err)
	require.NotNil(t, cl)

	scaffold.gInv.invch <- inventoryV1.Cluster{
		Nodes: multipleReplicasGenNodes(),
	}

	inv := waitForInventory(t, cl.ResultChan())
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(119525, 0, 2))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

// multipleReplicasGenNodes generates four nodes with following CPUs available
//
//	node1: 68780
//	node2: 68800
//	node3: 119525
//	node4: 119495
func multipleReplicasGenNodes() inventoryV1.Nodes {
	return inventoryV1.Nodes{
		{
			Name: "node1",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 119800, 51020, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 7760751097705, 8589934592, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node2",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 119800, 51000, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(2, 2, 0, resource.DecimalSI),
					Info: inventoryV1.GPUInfoS{
						{
							Vendor:     "nvidia",
							VendorID:   "10de",
							Name:       "a100",
							ModelID:    "20b5",
							Interface:  "pcie",
							MemorySize: "80Gi",
						},
						{
							Vendor:     "nvidia",
							VendorID:   "10de",
							Name:       "a100",
							ModelID:    "20b5",
							Interface:  "pcie",
							MemorySize: "80Gi",
						},
					},
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 7760751097705, 8589934592, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node3",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 119800, 275, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 7760751097705, 0, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node4",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 119800, 305, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 7760751097705, 0, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
		},
	}
}

func multipleReplicasGenReservations(cpuUnits, gpuUnits uint64, count uint32) *testReservation {
	var gpuAttributes attrtypes.Attributes
	if gpuUnits > 0 {
		gpuAttributes = append(gpuAttributes, attrtypes.Attribute{
			Key:   "vendor/nvidia/model/a100",
			Value: "true",
		})
	}
	return &testReservation{
		resources: dvbeta.GroupSpec{
			Name:         "bla",
			Requirements: attrtypes.PlacementRequirements{},
			Resources: dvbeta.ResourceUnits{
				{
					Resources: rtypes.Resources{
						ID: 1,
						CPU: &rtypes.CPU{
							Units: rtypes.NewResourceValue(cpuUnits),
						},
						GPU: &rtypes.GPU{
							Units:      rtypes.NewResourceValue(gpuUnits),
							Attributes: gpuAttributes,
						},
						Memory: &rtypes.Memory{
							Quantity: rtypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []rtypes.Storage{
							{
								Name:     "default",
								Quantity: rtypes.NewResourceValue(8 * unit.Gi),
							},
						},
					},
					Count: count,
				},
			},
		},
	}
}

func multipleSvcReplicasGenReservations(cpuUnits, gpuUnits uint64, count uint32) *testReservation {
	var gpuAttributes attrtypes.Attributes
	if gpuUnits > 0 {
		gpuAttributes = append(gpuAttributes, attrtypes.Attribute{
			Key:   "vendor/nvidia/model/a100",
			Value: "true",
		})
	}
	return &testReservation{
		resources: dvbeta.GroupSpec{
			Name:         "bla",
			Requirements: attrtypes.PlacementRequirements{},
			Resources: dvbeta.ResourceUnits{
				{
					Resources: rtypes.Resources{
						ID: 1,
						CPU: &rtypes.CPU{
							Units: rtypes.NewResourceValue(cpuUnits),
						},
						GPU: &rtypes.GPU{
							Units: rtypes.NewResourceValue(0),
						},
						Memory: &rtypes.Memory{
							Quantity: rtypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []rtypes.Storage{
							{
								Name:     "default",
								Quantity: rtypes.NewResourceValue(8 * unit.Gi),
							},
						},
					},
					Count: count,
				},
				{
					Resources: rtypes.Resources{
						ID: 2,
						CPU: &rtypes.CPU{
							Units: rtypes.NewResourceValue(cpuUnits),
						},
						GPU: &rtypes.GPU{
							Units:      rtypes.NewResourceValue(gpuUnits),
							Attributes: gpuAttributes,
						},
						Memory: &rtypes.Memory{
							Quantity: rtypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []rtypes.Storage{
							{
								Name:     "default",
								Quantity: rtypes.NewResourceValue(8 * unit.Gi),
							},
						},
					},
					Count: count,
				},
			},
		},
	}
}
