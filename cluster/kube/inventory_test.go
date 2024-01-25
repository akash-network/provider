package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/akash-api/go/node/types/unit"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	manualfake "k8s.io/client-go/rest/fake"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	akashclientfake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	kubernetesmocks "github.com/akash-network/provider/testutil/kubernetes_mock"
	corev1mocks "github.com/akash-network/provider/testutil/kubernetes_mock/typed/core/v1"
	storagev1mocks "github.com/akash-network/provider/testutil/kubernetes_mock/typed/storage/v1"
)

type testReservation struct {
	resources         dtypes.GroupSpec
	adjustedResources dtypes.ResourceUnits
	cparams           interface{}
}

var _ ctypes.Reservation = (*testReservation)(nil)

func (r *testReservation) OrderID() mtypes.OrderID {
	return mtypes.OrderID{}
}

func (r *testReservation) Resources() dtypes.ResourceGroup {
	return r.resources
}

func (r *testReservation) SetAllocatedResources(val dtypes.ResourceUnits) {
	r.adjustedResources = val
}

func (r *testReservation) GetAllocatedResources() dtypes.ResourceUnits {
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

type proxyCallback func(req *http.Request) (*http.Response, error)

type inventoryScaffold struct {
	kmock                   *kubernetesmocks.Interface
	amock                   *akashclientfake.Clientset
	coreV1Mock              *corev1mocks.CoreV1Interface
	storageV1Interface      *storagev1mocks.StorageV1Interface
	storageClassesInterface *storagev1mocks.StorageClassInterface
	nsInterface             *corev1mocks.NamespaceInterface
	nodeInterfaceMock       *corev1mocks.NodeInterface
	podInterfaceMock        *corev1mocks.PodInterface
	servicesInterfaceMock   *corev1mocks.ServiceInterface
	storageClassesList      *storagev1.StorageClassList
	nsList                  *v1.NamespaceList
}

func makeInventoryScaffold(proxycb proxyCallback) *inventoryScaffold {
	s := &inventoryScaffold{
		kmock:                   &kubernetesmocks.Interface{},
		amock:                   akashclientfake.NewSimpleClientset(),
		coreV1Mock:              &corev1mocks.CoreV1Interface{},
		storageV1Interface:      &storagev1mocks.StorageV1Interface{},
		storageClassesInterface: &storagev1mocks.StorageClassInterface{},
		nsInterface:             &corev1mocks.NamespaceInterface{},
		nodeInterfaceMock:       &corev1mocks.NodeInterface{},
		podInterfaceMock:        &corev1mocks.PodInterface{},
		servicesInterfaceMock:   &corev1mocks.ServiceInterface{},
		storageClassesList:      &storagev1.StorageClassList{},
		nsList:                  &v1.NamespaceList{},
	}

	fakeClient := &manualfake.RESTClient{
		GroupVersion:         appsv1.SchemeGroupVersion,
		NegotiatedSerializer: scheme.Codecs,
		Client:               manualfake.CreateHTTPClient(proxycb),
	}

	s.kmock.On("CoreV1").Return(s.coreV1Mock)

	s.coreV1Mock.On("RESTClient").Return(fakeClient)
	s.coreV1Mock.On("Namespaces").Return(s.nsInterface, nil)
	s.coreV1Mock.On("Nodes").Return(s.nodeInterfaceMock, nil)
	s.coreV1Mock.On("Pods", "" /* all namespaces */).Return(s.podInterfaceMock, nil)
	s.coreV1Mock.On("Services", mock.Anything).Return(s.servicesInterfaceMock, nil)

	s.nsInterface.On("List", mock.Anything, mock.Anything).Return(s.nsList, nil)

	s.kmock.On("StorageV1").Return(s.storageV1Interface)

	s.storageV1Interface.On("StorageClasses").Return(s.storageClassesInterface, nil)
	s.storageClassesInterface.On("List", mock.Anything, mock.Anything).Return(s.storageClassesList, nil)

	return s
}

func (s *inventoryScaffold) withInventoryService(fn func(func(string, ...interface{}) *mock.Call)) *inventoryScaffold {
	fn(s.servicesInterfaceMock.On)

	return s
}

func defaultInventoryService(on func(string, ...interface{}) *mock.Call) {
	svcList := &v1.ServiceList{
		Items: []v1.Service{
			{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "operator-inventory",
					Namespace: "akash-services",
				},
				Spec:   v1.ServiceSpec{},
				Status: v1.ServiceStatus{},
			},
		},
	}

	listOptions := metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=inventory" +
			",app.kubernetes.io/instance=inventory-service" +
			",app.kubernetes.io/component=operator" +
			",app.kubernetes.io/part-of=provider",
	}

	on("List", mock.Anything, listOptions).Return(svcList, nil)
}

func TestInventoryZero(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inventory)

	// The inventory was called and the kubernetes client says there are no nodes & no pods. Inventory
	// should be zero
	require.Len(t, inventory.Metrics().Nodes, 0)
}

func TestInventorySingleNodeNoPods(t *testing.T) {
	const expectedCPU = 13
	const expectedMemory = 14
	const expectedStorage = 15

	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: inventoryV1.Nodes{
				inventoryV1.Node{
					Name: "test",
					Resources: inventoryV1.NodeResources{
						CPU: inventoryV1.CPU{
							Quantity: inventoryV1.NewResourcePair(expectedCPU, 0, "m"),
						},
						Memory: inventoryV1.Memory{
							Quantity: inventoryV1.NewResourcePair(expectedMemory, 0, "M"),
						},
						GPU: inventoryV1.GPU{
							Quantity: inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
						},
						EphemeralStorage: inventoryV1.NewResourcePair(expectedStorage, 0, "M"),
						VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
						VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
					},
					Capabilities: inventoryV1.NodeCapabilities{},
				},
			},
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inventory)

	require.Len(t, inventory.Metrics().Nodes, 1)

	node := inventory.Metrics().Nodes[0]
	availableResources := node.Available
	// Multiply expected value by 1000 since millicpu is used
	require.Equal(t, uint64(expectedCPU*1000), availableResources.CPU)
	require.Equal(t, uint64(expectedMemory), availableResources.Memory)
	require.Equal(t, uint64(expectedStorage), availableResources.StorageEphemeral)
}

func TestInventorySingleNodeWithPods(t *testing.T) {
	const expectedCPU = 13
	const expectedMemory = 2048
	const expectedStorage = 4096

	const cpuPerContainer = 1
	const memoryPerContainer = 3
	const storagePerContainer = 17
	const totalContainers = 3

	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: inventoryV1.Nodes{
				inventoryV1.Node{
					Name: "test",
					Resources: inventoryV1.NodeResources{
						CPU: inventoryV1.CPU{
							Quantity: inventoryV1.NewResourcePair(expectedCPU, cpuPerContainer*totalContainers, "m"),
						},
						Memory: inventoryV1.Memory{
							Quantity: inventoryV1.NewResourcePair(expectedMemory, memoryPerContainer*totalContainers, "M"),
						},
						GPU: inventoryV1.GPU{
							Quantity: inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
						},
						EphemeralStorage: inventoryV1.NewResourcePair(expectedStorage, storagePerContainer*totalContainers, "M"),
						VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
						VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
					},
					Capabilities: inventoryV1.NodeCapabilities{},
				},
			},
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inventory)

	require.Len(t, inventory.Metrics().Nodes, 1)

	node := inventory.Metrics().Nodes[0]
	availableResources := node.Available
	// Multiply expected value by 1000 since millicpu is used
	assert.Equal(t, (uint64(expectedCPU)-(totalContainers*cpuPerContainer))*1000, availableResources.CPU)
	assert.Equal(t, uint64(expectedMemory)-totalContainers*memoryPerContainer, availableResources.Memory)
	assert.Equal(t, uint64(expectedStorage)-totalContainers*storagePerContainer, availableResources.StorageEphemeral)
}

func TestInventoryWithNodeError(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(&bytes.Buffer{})}, nil
	}).withInventoryService(func(on func(string, ...interface{}) *mock.Call) {
		on("List", mock.Anything, mock.Anything).Return(&v1.ServiceList{}, kerrors.NewNotFound(schema.GroupResource{}, "test-name"))
	})

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.Error(t, err)
	require.True(t, kerrors.IsNotFound(err))
	require.Nil(t, inventory)
}

func TestInventoryWithPodsError(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(&bytes.Buffer{})}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())

	require.Error(t, err)
	require.True(t, kerrors.IsServiceUnavailable(err))
	assert.Nil(t, inventory)
}

func TestInventoryMultipleReplicasFulFilled1(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
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
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 4))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled3(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68800, 0, 3))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled4(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(119495, 0, 2))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled5(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled6(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled7(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleSvcReplicasGenReservations(68700, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasOutOfCapacity1(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(70000, 0, 4))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

func TestInventoryMultipleReplicasOutOfCapacity2(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(100000, 0, 3))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

func TestInventoryMultipleReplicasOutOfCapacity4(t *testing.T) {
	s := makeInventoryScaffold(func(req *http.Request) (*http.Response, error) {
		inv := inventoryV1.Cluster{
			Nodes: multipleReplicasGenNodes(),
		}

		data, _ := json.Marshal(inv)

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBuffer(data))}, nil
	}).withInventoryService(defaultInventoryService)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
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
					Quantity: inventoryV1.NewResourcePairMilli(119800, 51020, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 8589934592, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node2",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 51000, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(2, 0, resource.DecimalSI),
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
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 8589934592, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node3",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 275, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 0, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
			},
		},
		{
			Name: "node4",
			Resources: inventoryV1.NodeResources{
				CPU: inventoryV1.CPU{
					Quantity: inventoryV1.NewResourcePairMilli(119800, 305, resource.DecimalSI),
				},
				Memory: inventoryV1.Memory{
					Quantity: inventoryV1.NewResourcePair(457317732352, 17495527424, resource.DecimalSI),
				},
				GPU: inventoryV1.GPU{
					Quantity: inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				},
				EphemeralStorage: inventoryV1.NewResourcePair(7760751097705, 0, resource.DecimalSI),
				VolumesAttached:  inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
				VolumesMounted:   inventoryV1.NewResourcePair(0, 0, resource.DecimalSI),
			},
		},
	}
}

func multipleReplicasGenReservations(cpuUnits, gpuUnits uint64, count uint32) *testReservation {
	var gpuAttributes atypes.Attributes
	if gpuUnits > 0 {
		gpuAttributes = append(gpuAttributes, atypes.Attribute{
			Key:   "vendor/nvidia/model/a100",
			Value: "true",
		})
	}
	return &testReservation{
		resources: dtypes.GroupSpec{
			Name:         "bla",
			Requirements: atypes.PlacementRequirements{},
			Resources: dtypes.ResourceUnits{
				{
					Resources: atypes.Resources{
						ID: 1,
						CPU: &atypes.CPU{
							Units: atypes.NewResourceValue(cpuUnits),
						},
						GPU: &atypes.GPU{
							Units:      atypes.NewResourceValue(gpuUnits),
							Attributes: gpuAttributes,
						},
						Memory: &atypes.Memory{
							Quantity: atypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []atypes.Storage{
							{
								Name:     "default",
								Quantity: atypes.NewResourceValue(8 * unit.Gi),
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
	var gpuAttributes atypes.Attributes
	if gpuUnits > 0 {
		gpuAttributes = append(gpuAttributes, atypes.Attribute{
			Key:   "vendor/nvidia/model/a100",
			Value: "true",
		})
	}
	return &testReservation{
		resources: dtypes.GroupSpec{
			Name:         "bla",
			Requirements: atypes.PlacementRequirements{},
			Resources: dtypes.ResourceUnits{
				{
					Resources: atypes.Resources{
						ID: 1,
						CPU: &atypes.CPU{
							Units: atypes.NewResourceValue(cpuUnits),
						},
						GPU: &atypes.GPU{
							Units: atypes.NewResourceValue(0),
						},
						Memory: &atypes.Memory{
							Quantity: atypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []atypes.Storage{
							{
								Name:     "default",
								Quantity: atypes.NewResourceValue(8 * unit.Gi),
							},
						},
					},
					Count: count,
				},
				{
					Resources: atypes.Resources{
						ID: 2,
						CPU: &atypes.CPU{
							Units: atypes.NewResourceValue(cpuUnits),
						},
						GPU: &atypes.GPU{
							Units:      atypes.NewResourceValue(gpuUnits),
							Attributes: gpuAttributes,
						},
						Memory: &atypes.Memory{
							Quantity: atypes.NewResourceValue(16 * unit.Gi),
						},
						Storage: []atypes.Storage{
							{
								Name:     "default",
								Quantity: atypes.NewResourceValue(8 * unit.Gi),
							},
						},
					},
					Count: count,
				},
			},
		},
	}
}
