package inventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
)

func newKubeNode(gpuCount int64) *corev1.Node {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-kube-node"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("16"),
				corev1.ResourceMemory:           resource.MustParse("64Gi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("500Gi"),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("16"),
				corev1.ResourceMemory:           resource.MustParse("64Gi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("500Gi"),
			},
		},
	}

	if gpuCount > 0 {
		gpuQty := *resource.NewQuantity(gpuCount, resource.DecimalSI)
		node.Status.Allocatable[builder.ResourceGPUNvidia] = gpuQty
		node.Status.Capacity[builder.ResourceGPUNvidia] = gpuQty
	}

	return node
}

func newInventoryNode() v1.Node {
	return v1.Node{
		Name: "test-node",
		Resources: v1.NodeResources{
			CPU: v1.CPU{
				Quantity: v1.NewResourcePairMilli(0, 0, 0, resource.DecimalSI),
			},
			GPU: v1.GPU{
				Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
			Memory: v1.Memory{
				Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			},
			EphemeralStorage: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesAttached:  v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesMounted:   v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
		},
	}
}

func newGPUPod(name string, gpuCount int64) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "gpu-workload",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							builder.ResourceGPUNvidia: *resource.NewQuantity(gpuCount, resource.DecimalSI),
						},
					},
				},
			},
		},
	}
}

func TestUpdateNodeInfo_GPUPresent(t *testing.T) {
	knode := newKubeNode(8)
	node := newInventoryNode()

	updateNodeInfo(knode, &node)

	assert.Equal(t, int64(8), node.Resources.GPU.Quantity.Allocatable.Value())
	assert.Equal(t, int64(8), node.Resources.GPU.Quantity.Capacity.Value())
}

func TestUpdateNodeInfo_GPUDisappears(t *testing.T) {
	knode := newKubeNode(8)
	node := newInventoryNode()

	// First update: GPU present
	updateNodeInfo(knode, &node)
	require.Equal(t, int64(8), node.Resources.GPU.Quantity.Allocatable.Value())

	// Second update: GPU disappeared (device plugin crashed)
	knodeNoGPU := newKubeNode(0) // gpuCount=0 means no GPU resource in map
	updateNodeInfo(knodeNoGPU, &node)

	assert.Equal(t, int64(0), node.Resources.GPU.Quantity.Allocatable.Value(),
		"GPU Allocatable must be 0 after device plugin disappears")
	assert.Equal(t, int64(0), node.Resources.GPU.Quantity.Capacity.Value(),
		"GPU Capacity must be 0 after device plugin disappears")
}

func TestUpdateNodeInfo_GPUDisappears_AvailableNoUnderflow(t *testing.T) {
	knode := newKubeNode(8)
	node := newInventoryNode()

	// GPU present, pods using GPUs
	updateNodeInfo(knode, &node)
	pod := newGPUPod("gpu-pod", 8)
	addPodAllocatedResources(&node, pod)
	require.Equal(t, int64(8), node.Resources.GPU.Quantity.Allocated.Value())

	// GPU disappears but pods still running
	knodeNoGPU := newKubeNode(0)
	updateNodeInfo(knodeNoGPU, &node)

	// Available must be 0, not an underflowed value
	avail := node.Resources.GPU.Quantity.Available()
	assert.Equal(t, int64(0), avail.Value(),
		"Available must be 0, not underflow")

	// Verify uint64 cast is safe
	val := uint64(avail.Value()) // nolint: gosec
	assert.Equal(t, uint64(0), val,
		"uint64 cast must be 0, not 2^64-8")
}

func TestAddPodAllocatedResources_GPU(t *testing.T) {
	node := newInventoryNode()
	node.Resources.GPU.Quantity.Allocatable.Set(8)

	pod := newGPUPod("gpu-pod", 4)
	addPodAllocatedResources(&node, pod)

	assert.Equal(t, int64(4), node.Resources.GPU.Quantity.Allocated.Value())
}

func TestAddPodAllocatedResources_GPUCappedAtAllocatable(t *testing.T) {
	node := newInventoryNode()
	node.Resources.GPU.Quantity.Allocatable.Set(2)

	// Pod requests more GPUs than allocatable
	pod := newGPUPod("gpu-pod", 8)
	addPodAllocatedResources(&node, pod)

	assert.Equal(t, int64(2), node.Resources.GPU.Quantity.Allocated.Value(),
		"Allocated must be capped at Allocatable, not exceed it")
}

func TestAddPodAllocatedResources_GPUZeroAllocatable(t *testing.T) {
	node := newInventoryNode()
	// GPU Allocatable remains 0 (device plugin not present)

	pod := newGPUPod("gpu-pod", 8)
	addPodAllocatedResources(&node, pod)

	assert.Equal(t, int64(0), node.Resources.GPU.Quantity.Allocated.Value(),
		"Allocated must stay 0 when Allocatable is 0")

	avail := node.Resources.GPU.Quantity.Available()
	assert.Equal(t, int64(0), avail.Value(),
		"Available must be 0, not underflow")
}

func TestAddPodAllocatedResources_MultiplePodsCapped(t *testing.T) {
	node := newInventoryNode()
	node.Resources.GPU.Quantity.Allocatable.Set(4)

	// First pod takes 3 GPUs — fits
	addPodAllocatedResources(&node, newGPUPod("pod-1", 3))
	assert.Equal(t, int64(3), node.Resources.GPU.Quantity.Allocated.Value())

	// Second pod requests 3 more — would exceed, must cap at 4
	addPodAllocatedResources(&node, newGPUPod("pod-2", 3))
	assert.Equal(t, int64(4), node.Resources.GPU.Quantity.Allocated.Value(),
		"Allocated must be capped at Allocatable after multiple pods")
}

func TestSubPodAllocatedResources_GPU(t *testing.T) {
	node := newInventoryNode()
	node.Resources.GPU.Quantity.Allocatable.Set(8)
	node.Resources.GPU.Quantity.Allocated.Set(4)

	pod := newGPUPod("gpu-pod", 4)
	subPodAllocatedResources(&node, pod)

	assert.Equal(t, int64(0), node.Resources.GPU.Quantity.Allocated.Value())
}

func TestSubPodAllocatedResources_ClampsToZero(t *testing.T) {
	node := newInventoryNode()
	node.Resources.GPU.Quantity.Allocated.Set(2)

	// Subtracting more than allocated
	pod := newGPUPod("gpu-pod", 4)
	subPodAllocatedResources(&node, pod)

	assert.Equal(t, int64(0), node.Resources.GPU.Quantity.Allocated.Value(),
		"subPodAllocatedResources must clamp to 0 via subAllocatedNLZ")
}
