package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryV1 "pkg.akt.dev/go/inventory/v1"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// interconnectCapNode returns a NodeResources / NodeCapabilities pair the per-bid
// tests can stamp custom GPU interconnect capacity onto.
func interconnectResourcePair(capacity, allocated int64) inventoryV1.ResourcePair {
	return inventoryV1.NewResourcePair(capacity, capacity, allocated, resource.DecimalSI)
}

// gpuInterconnectResource builds the per-resource shape that ResourceRequiresInterconnect
// inspects — units=N with the interconnect=true attribute.
func gpuInterconnectResource(units uint64) *rtypes.Resources {
	return &rtypes.Resources{
		GPU: &rtypes.GPU{
			Units: rtypes.NewResourceValue(units),
			Attributes: attrtypes.Attributes{
				{Key: AttributeGPUInterconnectKey, Value: "true"},
			},
		},
	}
}

func TestTryAdjustInterconnect_NoOptIn(t *testing.T) {
	// required=false → pure no-op, even on a node without any interconnect
	// capacity advertised. SchedulerParams must stay empty so DeepEqual
	// against a zero value at the bottom of tryAdjust still triggers.
	rp := interconnectResourcePair(0, 0)
	caps := inventoryV1.NodeCapabilities{}
	res := &rtypes.Resources{GPU: &rtypes.GPU{Units: rtypes.NewResourceValue(8)}}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustInterconnect(&rp, caps, res, sparams, "", false))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustInterconnect_NodeWithoutInterconnectRejected(t *testing.T) {
	// Resource opts in but the node is non-interconnect. The bid must move on to
	// the next node (false here flips nStatus to false in tryAdjust).
	rp := interconnectResourcePair(0, 0)
	caps := inventoryV1.NodeCapabilities{}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(8), sparams, "", true))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustInterconnect_FabricPinMismatchRejected(t *testing.T) {
	// Tenant demands InfiniBand, node only has RoCE.
	rp := interconnectResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectResourceName: "rdma/rdma_shared_device_eth",
		InterconnectFabric:       "roce",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(8), sparams, "infiniband", true))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustInterconnect_FabricPinMatchOK(t *testing.T) {
	rp := interconnectResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectResourceName: "rdma/rdma_shared_device_ib",
		InterconnectFabric:       "infiniband",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(8), sparams, "infiniband", true))
	require.NotNil(t, sparams.Resources)
	require.NotNil(t, sparams.Resources.Interconnect)
	require.Equal(t, &crd.SchedulerResourceInterconnect{
		Enabled:       true,
		Units:         8,
		ResourceName:  "rdma/rdma_shared_device_ib",
		Fabric:        "infiniband",
		NCCLHCAPrefix: "mlx5",
	}, sparams.Resources.Interconnect)

	// Allocation was actually charged (1:1 with GPU units).
	require.Equal(t, int64(0), rp.Available().Value())
}

func TestTryAdjustInterconnect_NoFabricPinAccepts(t *testing.T) {
	// Empty requiredFabric → any interconnect-capable node is acceptable.
	rp := interconnectResourcePair(4, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectResourceName: "rdma/rdma_shared_device_eth",
		InterconnectFabric:       "roce",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(4), sparams, "", true))
	require.Equal(t, "roce", sparams.Resources.Interconnect.Fabric)
}

func TestTryAdjustInterconnect_InsufficientCapacity(t *testing.T) {
	// 4 HCAs available, tenant wants 8 (1:1 to GPU.units).
	rp := interconnectResourcePair(4, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectResourceName: "rdma/rdma_shared_device_ib",
		InterconnectFabric:       "infiniband",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(8), sparams, "", true))
	require.Nil(t, sparams.Resources)
	// Available unchanged — SubNLZ refused to commit.
	require.Equal(t, int64(4), rp.Available().Value())
}

func TestTryAdjustInterconnect_NodeAdvertisesFabricButNoResourceName(t *testing.T) {
	// Half-configured node: fabric set but no kubelet extended resource
	// to allocate from. Must reject — the workload builder has nothing
	// to put under requests/limits.
	rp := interconnectResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectFabric:    "infiniband",
		NCCLHCAPrefix: "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustInterconnect(&rp, caps, gpuInterconnectResource(8), sparams, "", true))
}

// TestTryAdjustInterconnect_RequiredHonoredWithoutAttributes covers the count > 1
// case: tryAdjustGPU clobbered res.GPU.Attributes on a prior iteration,
// so the interconnect=true opt-in is gone from the resource we're handed. The
// caller still passes required=true (pulled from origResources) and we
// must stamp SchedulerParams.Resources.Interconnect exactly as on iteration 1,
// otherwise Adjust's DeepEqual rejects the bid with
// ErrGroupResourceMismatch.
func TestTryAdjustInterconnect_RequiredHonoredWithoutAttributes(t *testing.T) {
	rp := interconnectResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		InterconnectResourceName: "rdma/rdma_shared_device_ib",
		InterconnectFabric:       "infiniband",
		NCCLHCAPrefix:    "mlx5",
	}
	clobbered := &rtypes.Resources{
		GPU: &rtypes.GPU{
			Units: rtypes.NewResourceValue(8),
			// Attributes intentionally empty — simulates tryAdjustGPU's
			// prior mutation, where it replaced the slice with just the
			// synthesized vendor/model entry and the rdma attribute was
			// lost. ResourceRequiresInterconnect(*res) would now return false.
		},
	}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustInterconnect(&rp, caps, clobbered, sparams, "", true))
	require.NotNil(t, sparams.Resources)
	require.NotNil(t, sparams.Resources.Interconnect)
	require.Equal(t, uint64(8), sparams.Resources.Interconnect.Units)
}
