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

// rdmaCapNode returns a NodeResources / NodeCapabilities pair the per-bid
// tests can stamp custom RDMA capacity onto.
func rdmaResourcePair(capacity, allocated int64) inventoryV1.ResourcePair {
	return inventoryV1.NewResourcePair(capacity, capacity, allocated, resource.DecimalSI)
}

// gpuRDMAResource builds the per-resource shape that ResourceRequiresRDMA
// inspects — units=N with the rdma=true attribute.
func gpuRDMAResource(units uint64) *rtypes.Resources {
	return &rtypes.Resources{
		GPU: &rtypes.GPU{
			Units: rtypes.NewResourceValue(units),
			Attributes: attrtypes.Attributes{
				{Key: AttributeGPURDMAKey, Value: "true"},
			},
		},
	}
}

func TestTryAdjustRDMA_NoOptIn(t *testing.T) {
	// No rdma=true attribute → helper is a pure no-op, even on a node
	// without any RDMA capacity advertised. SchedulerParams must stay
	// empty so DeepEqual(zero) at the bottom of tryAdjust still triggers.
	rp := rdmaResourcePair(0, 0)
	caps := inventoryV1.NodeCapabilities{}
	res := &rtypes.Resources{GPU: &rtypes.GPU{Units: rtypes.NewResourceValue(8)}}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustRDMA(&rp, caps, res, sparams, ""))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustRDMA_NodeWithoutRDMARejected(t *testing.T) {
	// Resource opts in but the node is non-RDMA. The bid must move on to
	// the next node (false here flips nStatus to false in tryAdjust).
	rp := rdmaResourcePair(0, 0)
	caps := inventoryV1.NodeCapabilities{}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(8), sparams, ""))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustRDMA_FabricPinMismatchRejected(t *testing.T) {
	// Tenant demands InfiniBand, node only has RoCE.
	rp := rdmaResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		RDMAResourceName: "rdma/rdma_shared_device_eth",
		RDMAFabric:       "roce",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(8), sparams, "infiniband"))
	require.Nil(t, sparams.Resources)
}

func TestTryAdjustRDMA_FabricPinMatchOK(t *testing.T) {
	rp := rdmaResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		RDMAResourceName: "rdma/rdma_shared_device_ib",
		RDMAFabric:       "infiniband",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(8), sparams, "infiniband"))
	require.NotNil(t, sparams.Resources)
	require.NotNil(t, sparams.Resources.RDMA)
	require.Equal(t, &crd.SchedulerResourceRDMA{
		Enabled:       true,
		Units:         8,
		ResourceName:  "rdma/rdma_shared_device_ib",
		Fabric:        "infiniband",
		NCCLHCAPrefix: "mlx5",
	}, sparams.Resources.RDMA)

	// Allocation was actually charged (1:1 with GPU units).
	require.Equal(t, int64(0), rp.Available().Value())
}

func TestTryAdjustRDMA_NoFabricPinAccepts(t *testing.T) {
	// Empty requiredFabric → any RDMA-capable node is acceptable.
	rp := rdmaResourcePair(4, 0)
	caps := inventoryV1.NodeCapabilities{
		RDMAResourceName: "rdma/rdma_shared_device_eth",
		RDMAFabric:       "roce",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.True(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(4), sparams, ""))
	require.Equal(t, "roce", sparams.Resources.RDMA.Fabric)
}

func TestTryAdjustRDMA_InsufficientCapacity(t *testing.T) {
	// 4 HCAs available, tenant wants 8 (1:1 to GPU.units).
	rp := rdmaResourcePair(4, 0)
	caps := inventoryV1.NodeCapabilities{
		RDMAResourceName: "rdma/rdma_shared_device_ib",
		RDMAFabric:       "infiniband",
		NCCLHCAPrefix:    "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(8), sparams, ""))
	require.Nil(t, sparams.Resources)
	// Available unchanged — SubNLZ refused to commit.
	require.Equal(t, int64(4), rp.Available().Value())
}

func TestTryAdjustRDMA_NodeAdvertisesFabricButNoResourceName(t *testing.T) {
	// Half-configured node: fabric set but no kubelet extended resource
	// to allocate from. Must reject — the workload builder has nothing
	// to put under requests/limits.
	rp := rdmaResourcePair(8, 0)
	caps := inventoryV1.NodeCapabilities{
		RDMAFabric:    "infiniband",
		NCCLHCAPrefix: "mlx5",
	}
	sparams := &crd.SchedulerParams{}

	require.False(t, tryAdjustRDMA(&rp, caps, gpuRDMAResource(8), sparams, ""))
}
