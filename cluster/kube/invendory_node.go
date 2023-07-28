package kube

import (
	"fmt"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

const (
	runtimeClassNvidia = "nvidia"
)

type node struct {
	id               string
	cpu              resourcePair
	gpu              resourcePair
	memory           resourcePair
	ephemeralStorage resourcePair
	volumesAttached  resourcePair
	volumesMounted   resourcePair
	capabilities     *crd.NodeInfoCapabilities
}

func newNode(nodeStatus *corev1.NodeStatus, capabilities *crd.NodeInfoCapabilities) *node {
	mzero := resource.NewMilliQuantity(0, resource.DecimalSI)
	zero := resource.NewQuantity(0, resource.DecimalSI)

	gpu := *resource.NewQuantity(0, resource.DecimalSI)
	if capabilities != nil {
		var resourceName corev1.ResourceName
		switch capabilities.GPU.Vendor {
		case builder.GPUVendorNvidia:
			resourceName = builder.ResourceGPUNvidia
		case builder.GPUVendorAMD:
			resourceName = builder.ResourceGPUAMD
		}

		gpu = nodeStatus.Allocatable.Name(resourceName, resource.DecimalSI).DeepCopy()
	}

	nd := &node{
		cpu:              newResourcePair(nodeStatus.Allocatable.Cpu().DeepCopy(), mzero.DeepCopy()),
		gpu:              newResourcePair(gpu, zero.DeepCopy()),
		memory:           newResourcePair(nodeStatus.Allocatable.Memory().DeepCopy(), zero.DeepCopy()),
		ephemeralStorage: newResourcePair(nodeStatus.Allocatable.StorageEphemeral().DeepCopy(), zero.DeepCopy()),
		volumesAttached:  newResourcePair(*resource.NewQuantity(int64(len(nodeStatus.VolumesAttached)), resource.DecimalSI), zero.DeepCopy()),
		capabilities:     capabilities,
	}

	return nd
}

func (nd *node) addAllocatedResources(rl corev1.ResourceList) {
	for name, quantity := range rl {
		switch name {
		case corev1.ResourceCPU:
			nd.cpu.allocated.Add(quantity)
		case corev1.ResourceMemory:
			nd.memory.allocated.Add(quantity)
		case corev1.ResourceEphemeralStorage:
			nd.ephemeralStorage.allocated.Add(quantity)
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			nd.gpu.allocated.Add(quantity)
		}
	}
}

func (nd *node) dup() *node {
	res := &node{
		id:               nd.id,
		cpu:              *nd.cpu.dup(),
		gpu:              *nd.gpu.dup(),
		memory:           *nd.memory.dup(),
		ephemeralStorage: *nd.ephemeralStorage.dup(),
		volumesAttached:  *nd.volumesAttached.dup(),
		volumesMounted:   *nd.volumesMounted.dup(),
		capabilities:     nd.capabilities.DeepCopy(),
	}

	return res
}

func (nd *node) tryAdjustCPU(res *types.CPU) bool {
	return nd.cpu.subMilliNLZ(res.Units)
}

func (nd *node) tryAdjustGPU(res *types.GPU, sparams *crd.SchedulerParams) bool {
	if res.Units.Value() == 0 {
		return true
	}

	// GPUs cannot be reserved until node capabilities available
	if nd.capabilities == nil {
		return false
	}

	attrs, err := ctypes.ParseGPUAttributes(res.Attributes)
	if err != nil {
		return false
	}

	models, match := attrs[nd.capabilities.GPU.Vendor]
	if !match {
		return false
	}

	var model string
	for _, m := range models {
		if m == nd.capabilities.GPU.Model || m == "*" {
			model = nd.capabilities.GPU.Model
			break
		}
	}

	if model == "" {
		return false
	}

	if !nd.gpu.subNLZ(res.Units) {
		return false
	}

	sParamsEnsureGPU(sparams)
	sparams.Resources.GPU.Vendor = nd.capabilities.GPU.Vendor
	sparams.Resources.GPU.Model = model

	switch nd.capabilities.GPU.Vendor {
	case builder.GPUVendorNvidia:
		sparams.RuntimeClass = runtimeClassNvidia
	default:
	}

	res.Attributes = types.Attributes{
		{
			Key:   fmt.Sprintf("vendor/%s/model/%s", nd.capabilities.GPU.Vendor, model),
			Value: "true",
		},
	}

	return true
}

func sParamsEnsureGPU(sparams *crd.SchedulerParams) {
	sParamsEnsureResources(sparams)

	if sparams.Resources.GPU == nil {
		sparams.Resources.GPU = &crd.SchedulerResourceGPU{}
	}
}

func sParamsEnsureResources(sparams *crd.SchedulerParams) {
	if sparams.Resources == nil {
		sparams.Resources = &crd.SchedulerResources{}
	}
}

func (nd *node) tryAdjustMemory(res *types.Memory) bool {
	return nd.memory.subNLZ(res.Quantity)
}

func (nd *node) tryAdjustEphemeralStorage(res *types.Storage) bool {
	return nd.ephemeralStorage.subNLZ(res.Quantity)
}

// nolint: unused
func (nd *node) tryAdjustVolumesAttached(res types.ResourceValue) bool {
	return nd.volumesAttached.subNLZ(res)
}

func (cn clusterNodes) dup() clusterNodes {
	ret := make(clusterNodes)

	for name, nd := range cn {
		ret[name] = nd.dup()
	}
	return ret
}
