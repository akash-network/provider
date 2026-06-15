package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryV1 "pkg.akt.dev/go/inventory/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/node/types/unit"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

// AKT-443 — bid-engine group-aware Adjust. tryAdjust now refuses to place
// two resources from the same `interconnect_group` on the same node so the
// workload builder's hard pod anti-affinity remains satisfiable at deploy
// time. These tests exercise the rejection AND the success cases plus
// regressions (no-group, distinct-group, count > 1).

// interconnectGroupNode produces a single node with 8 interconnect-capable A100 GPUs
// (matches the configuration of every node on the production test
// cluster). Returns a sized node so each test can be explicit about how
// many nodes the cluster contains.
func interconnectGroupNode(name string) inventoryV1.Node {
	const gpus = 8
	gpuInfo := make(inventoryV1.GPUInfoS, 0, gpus)
	for i := 0; i < gpus; i++ {
		gpuInfo = append(gpuInfo, inventoryV1.GPUInfo{
			Vendor: "nvidia", VendorID: "10de",
			Name: "a100", ModelID: "20b5",
			Interface: "sxm", MemorySize: "80Gi",
		})
	}
	return inventoryV1.Node{
		Name: name,
		Resources: inventoryV1.NodeResources{
			CPU:    inventoryV1.CPU{Quantity: inventoryV1.NewResourcePairMilli(200000, 200000, 0, resource.DecimalSI)},
			Memory: inventoryV1.Memory{Quantity: inventoryV1.NewResourcePair(1024*unit.Gi, 1024*unit.Gi, 0, resource.DecimalSI)},
			GPU: inventoryV1.GPU{
				Quantity: inventoryV1.NewResourcePair(gpus, gpus, 0, resource.DecimalSI),
				Info:     gpuInfo,
			},
			GPUInterconnect:  inventoryV1.NewResourcePair(63, 63, 0, resource.DecimalSI),
			EphemeralStorage: inventoryV1.NewResourcePair(8*unit.Ti, 8*unit.Ti, 0, resource.DecimalSI),
			VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
		},
		Capabilities: inventoryV1.NodeCapabilities{
			InterconnectResourceName: "rdma/rdma_shared_device_ib",
			InterconnectFabric:       "infiniband",
			NCCLHCAPrefix:    "mlx5",
		},
	}
}

func interconnectGroupCluster(n int) inventoryV1.Cluster {
	nodes := make(inventoryV1.Nodes, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, interconnectGroupNode("ic-"+string(rune('a'+i))))
	}
	return inventoryV1.Cluster{Nodes: nodes}
}

// interconnectGroupResource builds one ResourceUnit for an interconnect service. group is
// the SDL gpu.attributes.interconnect_group value ("" for no group).
func interconnectGroupResource(id uint32, count uint32, group string) dvbeta.ResourceUnit {
	attrs := attrtypes.Attributes{
		{Key: "interconnect", Value: "true"},
		{Key: "vendor/nvidia/model/a100/ram/80Gi/interface/sxm", Value: "true"},
	}
	if group != "" {
		attrs = append(attrs, attrtypes.Attribute{Key: "interconnect_group", Value: group})
	}
	return dvbeta.ResourceUnit{
		Resources: rtypes.Resources{
			ID: id,
			CPU: &rtypes.CPU{
				Units: rtypes.NewResourceValue(32000),
			},
			GPU: &rtypes.GPU{
				Units:      rtypes.NewResourceValue(8),
				Attributes: attrs,
			},
			Memory: &rtypes.Memory{
				Quantity: rtypes.NewResourceValue(128 * unit.Gi),
			},
			Storage: []rtypes.Storage{{
				Name:     "default",
				Quantity: rtypes.NewResourceValue(8 * unit.Gi),
			}},
		},
		Count: count,
	}
}

// interconnectGroupReservation wraps several ResourceUnits with the placement
// requirements an interconnect SDL declares (capabilities/gpu-interconnect=true +
// fabric/infiniband=true), so PlacementRequiredFabric returns
// "infiniband".
func interconnectGroupReservation(units ...dvbeta.ResourceUnit) *testReservation {
	return &testReservation{
		resources: dvbeta.GroupSpec{
			Name: "interconnect",
			Requirements: attrtypes.PlacementRequirements{
				Attributes: attrtypes.Attributes{
					{Key: "capabilities/gpu-interconnect", Value: "true"},
					{Key: "capabilities/gpu-interconnect/fabric/infiniband", Value: "true"},
				},
			},
			Resources: units,
		},
	}
}

// Two services in pair0, count=1 each. 2 nodes, 8 GPUs each. Bid should
// succeed because each service can claim a distinct node.
func TestAdjust_InterconnectGroup_TwoServicesOneEach_TwoNodes_Success(t *testing.T) {
	inv := newInventory(interconnectGroupCluster(2))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 1, "pair0"),
		interconnectGroupResource(2, 1, "pair0"),
	)

	require.NoError(t, inv.Adjust(res))
}

// Same shape, but only one node. Both peers cannot land on it
// (anti-affinity would be unschedulable), so Adjust must reject.
func TestAdjust_InterconnectGroup_TwoServicesOneEach_OneNode_Rejected(t *testing.T) {
	inv := newInventory(interconnectGroupCluster(1))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 1, "pair0"),
		interconnectGroupResource(2, 1, "pair0"),
	)

	err := inv.Adjust(res)
	require.ErrorIs(t, err, ctypes.ErrInsufficientCapacity,
		"two peers in pair0 cannot fit on a single node")
}

// Single service, count=4 in pair0. 4 nodes. Each replica claims a
// distinct node — bid succeeds.
func TestAdjust_InterconnectGroup_SingleServiceCount4_FourNodes_Success(t *testing.T) {
	inv := newInventory(interconnectGroupCluster(4))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 4, "pair0"),
	)

	require.NoError(t, inv.Adjust(res))
}

// Same shape, only 3 nodes. 4th replica has no clean node — reject.
func TestAdjust_InterconnectGroup_SingleServiceCount4_ThreeNodes_Rejected(t *testing.T) {
	inv := newInventory(interconnectGroupCluster(3))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 4, "pair0"),
	)

	err := inv.Adjust(res)
	require.ErrorIs(t, err, ctypes.ErrInsufficientCapacity,
		"4 replicas of pair0 cannot fit on 3 nodes")
}

// Two services in DIFFERENT groups. Groups are independent — peers from
// pair0 must not collide, peers from pair1 must not collide, but pair0
// and pair1 can share nodes. With 2 nodes and one service per group at
// count=1, both groups fit (each occupies one node, the other group can
// double-up). This proves groups don't bleed into each other.
func TestAdjust_InterconnectGroup_DistinctGroupsIndependent(t *testing.T) {
	inv := newInventory(interconnectGroupCluster(2))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 1, "pair0"),
		interconnectGroupResource(2, 1, "pair1"),
	)

	require.NoError(t, inv.Adjust(res))
}

// No interconnect_group set anywhere. Regression: existing single-service interconnect
// bids on a cluster with 1 node continue to work — the group-aware gate
// only fires when a non-empty group is set.
func TestAdjust_InterconnectGroup_EmptyGroupAllowsCoLocation(t *testing.T) {
	// One node has 8 GPUs — enough for one service with count=1.
	inv := newInventory(interconnectGroupCluster(1))
	res := interconnectGroupReservation(
		interconnectGroupResource(1, 1, ""),
	)

	require.NoError(t, inv.Adjust(res))
}
