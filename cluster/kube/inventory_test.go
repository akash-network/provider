package kube

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	"github.com/akash-network/akash-api/go/node/types/unit"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/node/testutil"

	"github.com/akash-network/provider/cluster/kube/builder"
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

func (r *testReservation) Allocated() bool {
	return false
}

func (r *testReservation) SetClusterParams(val interface{}) {
	r.cparams = val
}

func (r *testReservation) ClusterParams() interface{} {
	return r.cparams
}

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

func makeInventoryScaffold() *inventoryScaffold {
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

	s.kmock.On("CoreV1").Return(s.coreV1Mock)

	s.coreV1Mock.On("Namespaces").Return(s.nsInterface, nil)
	s.coreV1Mock.On("Nodes").Return(s.nodeInterfaceMock, nil)
	s.coreV1Mock.On("Pods", "" /* all namespaces */).Return(s.podInterfaceMock, nil)
	s.coreV1Mock.On("Services", "" /* all namespaces */).Return(s.servicesInterfaceMock, nil)

	s.nsInterface.On("List", mock.Anything, mock.Anything).Return(s.nsList, nil)

	s.kmock.On("StorageV1").Return(s.storageV1Interface)

	s.storageV1Interface.On("StorageClasses").Return(s.storageClassesInterface, nil)
	s.storageClassesInterface.On("List", mock.Anything, mock.Anything).Return(s.storageClassesList, nil)

	s.servicesInterfaceMock.On("List", mock.Anything, mock.Anything).Return(&v1.ServiceList{}, nil)

	return s
}

func TestInventoryZero(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{}
	listOptions := metav1.ListOptions{}
	s.nodeInterfaceMock.On("List", mock.Anything, listOptions).Return(nodeList, nil)

	podList := &v1.PodList{}
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inventory)

	// The inventory was called and the kubernetes client says there are no nodes & no pods. Inventory
	// should be zero
	require.Len(t, inventory.Metrics().Nodes, 0)

	podListOptionsInCall := s.podInterfaceMock.Calls[0].Arguments[1].(metav1.ListOptions)
	require.Equal(t, "status.phase==Running", podListOptionsInCall.FieldSelector)
}

func TestInventorySingleNodeNoPods(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{}
	nodeList.Items = make([]v1.Node, 1)

	nodeResourceList := make(v1.ResourceList)
	const expectedCPU = 13
	cpuQuantity := resource.NewQuantity(expectedCPU, "m")
	nodeResourceList[v1.ResourceCPU] = *cpuQuantity

	const expectedMemory = 14
	memoryQuantity := resource.NewQuantity(expectedMemory, "M")
	nodeResourceList[v1.ResourceMemory] = *memoryQuantity

	const expectedStorage = 15
	ephemeralStorageQuantity := resource.NewQuantity(expectedStorage, "M")
	nodeResourceList[v1.ResourceEphemeralStorage] = *ephemeralStorageQuantity

	nodeConditions := make([]v1.NodeCondition, 1)
	nodeConditions[0] = v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionTrue,
	}

	nodeList.Items[0] = v1.Node{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec:       v1.NodeSpec{},
		Status: v1.NodeStatus{
			Allocatable: nodeResourceList,
			Conditions:  nodeConditions,
		},
	}

	listOptions := metav1.ListOptions{}
	s.nodeInterfaceMock.On("List", mock.Anything, listOptions).Return(nodeList, nil)

	podList := &v1.PodList{}
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

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
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{}
	nodeList.Items = make([]v1.Node, 1)

	nodeResourceList := make(v1.ResourceList)
	const expectedCPU = 13
	cpuQuantity := resource.NewQuantity(expectedCPU, "m")
	nodeResourceList[v1.ResourceCPU] = *cpuQuantity

	const expectedMemory = 2048
	memoryQuantity := resource.NewQuantity(expectedMemory, "M")
	nodeResourceList[v1.ResourceMemory] = *memoryQuantity

	const expectedStorage = 4096
	ephemeralStorageQuantity := resource.NewQuantity(expectedStorage, "M")
	nodeResourceList[v1.ResourceEphemeralStorage] = *ephemeralStorageQuantity

	nodeConditions := make([]v1.NodeCondition, 1)
	nodeConditions[0] = v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionTrue,
	}

	nodeList.Items[0] = v1.Node{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec:       v1.NodeSpec{},
		Status: v1.NodeStatus{
			Allocatable: nodeResourceList,
			Conditions:  nodeConditions,
		},
	}

	listOptions := metav1.ListOptions{}
	s.nodeInterfaceMock.On("List", mock.Anything, listOptions).Return(nodeList, nil)

	const cpuPerContainer = 1
	const memoryPerContainer = 3
	const storagePerContainer = 17
	// Define two pods
	pods := make([]v1.Pod, 2)
	// First pod has 1 container
	podContainers := make([]v1.Container, 1)
	containerRequests := make(v1.ResourceList)
	cpuQuantity.SetMilli(cpuPerContainer)
	containerRequests[v1.ResourceCPU] = *cpuQuantity

	memoryQuantity = resource.NewQuantity(memoryPerContainer, "M")
	containerRequests[v1.ResourceMemory] = *memoryQuantity

	ephemeralStorageQuantity = resource.NewQuantity(storagePerContainer, "M")
	containerRequests[v1.ResourceEphemeralStorage] = *ephemeralStorageQuantity

	podContainers[0] = v1.Container{
		Resources: v1.ResourceRequirements{
			Limits:   nil,
			Requests: containerRequests,
		},
	}
	pods[0] = v1.Pod{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: v1.PodSpec{
			Containers: podContainers,
		},
		Status: v1.PodStatus{},
	}

	// Define 2nd pod with multiple containers
	podContainers = make([]v1.Container, 2)
	for i := range podContainers {
		containerRequests := make(v1.ResourceList)
		cpuQuantity.SetMilli(cpuPerContainer)
		containerRequests[v1.ResourceCPU] = *cpuQuantity

		memoryQuantity = resource.NewQuantity(memoryPerContainer, "M")
		containerRequests[v1.ResourceMemory] = *memoryQuantity

		ephemeralStorageQuantity = resource.NewQuantity(storagePerContainer, "M")
		containerRequests[v1.ResourceEphemeralStorage] = *ephemeralStorageQuantity

		// Container limits are enforced by kubernetes as absolute limits, but not
		// used when considering inventory since overcommit is possible in a kubernetes cluster
		// Set limits to any value larger than requests in this test since it should not change
		// the value returned  by the code
		containerLimits := make(v1.ResourceList)

		for k, v := range containerRequests {
			replacementV := resource.NewQuantity(0, "")
			replacementV.Set(v.Value() * int64(testutil.RandRangeInt(2, 100)))
			containerLimits[k] = *replacementV
		}

		podContainers[i] = v1.Container{
			Resources: v1.ResourceRequirements{
				Limits:   containerLimits,
				Requests: containerRequests,
			},
		}
	}
	pods[1] = v1.Pod{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		Spec: v1.PodSpec{
			Containers: podContainers,
		},
		Status: v1.PodStatus{},
	}

	podList := &v1.PodList{
		Items: pods,
	}

	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inventory)

	require.Len(t, inventory.Metrics().Nodes, 1)

	node := inventory.Metrics().Nodes[0]
	availableResources := node.Available
	// Multiply expected value by 1000 since millicpu is used
	require.Equal(t, uint64(expectedCPU*1000)-3*cpuPerContainer, availableResources.CPU)
	require.Equal(t, uint64(expectedMemory)-3*memoryPerContainer, availableResources.Memory)
	require.Equal(t, uint64(expectedStorage)-3*storagePerContainer, availableResources.StorageEphemeral)
}

var errForTest = errors.New("error in test")

func TestInventoryWithNodeError(t *testing.T) {
	s := makeInventoryScaffold()

	listOptions := metav1.ListOptions{}
	s.nodeInterfaceMock.On("List", mock.Anything, listOptions).Return(nil, errForTest)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, errForTest))
	require.Nil(t, inventory)
}

func TestInventoryWithPodsError(t *testing.T) {
	s := makeInventoryScaffold()

	listOptions := metav1.ListOptions{}
	nodeList := &v1.NodeList{}
	s.nodeInterfaceMock.On("List", mock.Anything, listOptions).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nil, errForTest)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inventory, err := clientInterface.Inventory(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, errForTest))
	require.Nil(t, inventory)
}

func TestInventoryMultipleReplicasFulFilled1(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

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
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 4))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled3(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68800, 0, 3))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled4(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(119495, 0, 2))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled5(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 0, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled6(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(68780, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasFulFilled7(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleSvcReplicasGenReservations(68700, 1, 1))
	require.NoError(t, err)
}

func TestInventoryMultipleReplicasOutOfCapacity1(t *testing.T) {
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

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
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

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
	s := makeInventoryScaffold()

	nodeList := &v1.NodeList{
		Items: multipleReplicasGenNodes(),
	}

	podList := &v1.PodList{Items: []v1.Pod{}}

	s.nodeInterfaceMock.On("List", mock.Anything, mock.Anything).Return(nodeList, nil)
	s.podInterfaceMock.On("List", mock.Anything, mock.Anything).Return(podList, nil)

	clientInterface := clientForTest(t, s.kmock, s.amock)
	inv, err := clientInterface.Inventory(context.Background())
	require.NoError(t, err)
	require.NotNil(t, inv)
	require.Len(t, inv.Metrics().Nodes, 4)

	err = inv.Adjust(multipleReplicasGenReservations(119525, 0, 2))
	require.Error(t, err)
	require.EqualError(t, ctypes.ErrInsufficientCapacity, err.Error())
}

func TestParseCapabilities(t *testing.T) {
	type testCase struct {
		labels          map[string]string
		expCapabilities *crd.NodeInfoCapabilities
	}

	tests := []testCase{
		{
			labels: map[string]string{
				"akash.network/capabilities.gpu.vendor.nvidia.model.a100": "true",
			},
			expCapabilities: &crd.NodeInfoCapabilities{
				GPU: crd.GPUCapabilities{
					Vendor: "nvidia",
					Model:  "a100",
				},
			},
		},
	}

	for _, test := range tests {
		caps := parseNodeCapabilities(test.labels, nil)
		require.Equal(t, test.expCapabilities, caps)
	}
}

// multipleReplicasGenNodes generates four nodes with following CPUs available
//
//	node1: 68780
//	node2: 68800
//	node3: 119525
//	node4: 119495
func multipleReplicasGenNodes() []v1.Node {
	nodeCapacity := make(v1.ResourceList)
	nodeCapacity[v1.ResourceCPU] = *(resource.NewMilliQuantity(119800, resource.DecimalSI))
	nodeCapacity[v1.ResourceMemory] = *(resource.NewQuantity(474813259776, resource.DecimalSI))
	nodeCapacity[v1.ResourceEphemeralStorage] = *(resource.NewQuantity(7760751097705, resource.DecimalSI))

	nodeConditions := make([]v1.NodeCondition, 1)
	nodeConditions[0] = v1.NodeCondition{
		Type:   v1.NodeReady,
		Status: v1.ConditionTrue,
	}

	return []v1.Node{
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
			},
			Spec: v1.NodeSpec{},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:              *(resource.NewMilliQuantity(68780, resource.DecimalSI)),
					v1.ResourceMemory:           *(resource.NewQuantity(457317732352, resource.DecimalSI)),
					v1.ResourceEphemeralStorage: *(resource.NewQuantity(7752161163113, resource.DecimalSI)),
				},
				Capacity:   nodeCapacity,
				Conditions: nodeConditions,
			},
		},
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node2",
				Labels: map[string]string{
					"akash.network/capabilities.gpu.vendor.nvidia.model.a100": "true",
				},
			},
			Spec: v1.NodeSpec{},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:              *(resource.NewMilliQuantity(68800, resource.DecimalSI)),
					builder.ResourceGPUNvidia:   *(resource.NewQuantity(2, resource.DecimalSI)),
					v1.ResourceMemory:           *(resource.NewQuantity(457328218112, resource.DecimalSI)),
					v1.ResourceEphemeralStorage: *(resource.NewQuantity(7752161163113, resource.DecimalSI)),
				},
				Capacity:   nodeCapacity,
				Conditions: nodeConditions,
			},
		},
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node3",
			},
			Spec: v1.NodeSpec{},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:              *(resource.NewMilliQuantity(119525, resource.DecimalSI)),
					v1.ResourceMemory:           *(resource.NewQuantity(474817923072, resource.DecimalSI)),
					v1.ResourceEphemeralStorage: *(resource.NewQuantity(7760751097705, resource.DecimalSI)),
				},
				Capacity:   nodeCapacity,
				Conditions: nodeConditions,
			},
		},
		{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node4",
			},
			Spec: v1.NodeSpec{},
			Status: v1.NodeStatus{
				Allocatable: v1.ResourceList{
					v1.ResourceCPU:              *(resource.NewMilliQuantity(119495, resource.DecimalSI)),
					v1.ResourceMemory:           *(resource.NewQuantity(474753923072, resource.DecimalSI)),
					v1.ResourceEphemeralStorage: *(resource.NewQuantity(7760751097705, resource.DecimalSI)),
				},
				Capacity:   nodeCapacity,
				Conditions: nodeConditions,
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
