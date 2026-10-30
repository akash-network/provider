package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	inventoryV1 "pkg.akt.dev/go/inventory/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

// archNode is a node with cpuMilli CPU whose inventory reports arch, or reports no arch
// when arch is empty, as an inventory operator that predates arch reporting does.
func archNode(name string, cpuMilli int64, arch string) inventoryV1.Node {
	cpu := inventoryV1.CPU{
		Quantity: inventoryV1.NewResourcePairMilli(cpuMilli, cpuMilli, 0, resource.DecimalSI),
	}
	if arch != "" {
		cpu.Info = inventoryV1.CPUInfoS{{ID: "0", Vcores: 1, Arch: arch}}
	}

	return inventoryV1.Node{
		Name: name,
		Resources: inventoryV1.NodeResources{
			CPU:              cpu,
			Memory:           inventoryV1.Memory{Quantity: inventoryV1.NewResourcePair(1024, 1024, 0, resource.DecimalSI)},
			GPU:              inventoryV1.GPU{Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI)},
			EphemeralStorage: inventoryV1.NewResourcePair(100, 100, 0, resource.DecimalSI),
			VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
		},
	}
}

// archReservation asks for count replicas of cpuMilli CPU each whose SDL names arch, or
// names no arch when arch is empty.
func archReservation(cpuMilli uint64, arch string, count uint32) *testReservation {
	cpu := &rtypes.CPU{Units: rtypes.NewResourceValue(cpuMilli)}
	if arch != "" {
		cpu.Attributes = attrtypes.Attributes{{Key: "arch", Value: arch}}
	}

	return &testReservation{
		resources: dvbeta.GroupSpec{
			Name: "group",
			Resources: dvbeta.ResourceUnits{
				{
					Resources: rtypes.Resources{
						ID:     1,
						CPU:    cpu,
						GPU:    &rtypes.GPU{Units: rtypes.NewResourceValue(0)},
						Memory: &rtypes.Memory{Quantity: rtypes.NewResourceValue(1)},
					},
					Count: count,
				},
			},
		},
	}
}

// An SDL that names no CPU arch must be placed on any node, whatever its arch, so a
// single-node or homogeneous cluster of any arch takes it. A named arch only narrows
// the nodes it may be placed on.
func TestInventoryAdjustCPUArch(t *testing.T) {
	tests := []struct {
		name  string
		nodes inventoryV1.Nodes
		arch  string
		// replicas of 500m CPU each the SDL asks for; 0 means 1.
		replicas uint32
		// wantAllocated is the CPU, in millicores, each node has allocated afterwards.
		wantAllocated []int64
		wantErr       error
	}{
		{
			name:          "single arm64 node takes an SDL without arch",
			nodes:         inventoryV1.Nodes{archNode("arm", 1000, "arm64")},
			wantAllocated: []int64{500},
		},
		{
			name:          "single amd64 node takes an SDL without arch",
			nodes:         inventoryV1.Nodes{archNode("amd", 1000, "amd64")},
			wantAllocated: []int64{500},
		},
		{
			name:          "homogeneous arm64 cluster takes an SDL without arch",
			nodes:         inventoryV1.Nodes{archNode("arm-a", 1000, "arm64"), archNode("arm-b", 1000, "arm64")},
			wantAllocated: []int64{500, 0},
		},
		{
			name:          "homogeneous arm64 cluster spreads replicas of an SDL without arch across nodes",
			nodes:         inventoryV1.Nodes{archNode("arm-a", 600, "arm64"), archNode("arm-b", 600, "arm64")},
			replicas:      2,
			wantAllocated: []int64{500, 500},
		},
		{
			name:          "mixed cluster places an SDL without arch on the node with room",
			nodes:         inventoryV1.Nodes{archNode("amd", 100, "amd64"), archNode("arm", 1000, "arm64")},
			wantAllocated: []int64{0, 500},
		},
		{
			name:          "mixed cluster spreads replicas of an SDL without arch across archs",
			nodes:         inventoryV1.Nodes{archNode("amd", 600, "amd64"), archNode("arm", 600, "arm64")},
			replicas:      2,
			wantAllocated: []int64{500, 500},
		},
		{
			name:          "named arch is placed on a node of that arch only",
			nodes:         inventoryV1.Nodes{archNode("amd", 1000, "amd64"), archNode("arm", 1000, "arm64")},
			arch:          "arm64",
			wantAllocated: []int64{0, 500},
		},
		{
			name:    "named arch is refused when only other-arch nodes have room",
			nodes:   inventoryV1.Nodes{archNode("amd", 1000, "amd64"), archNode("arm", 100, "arm64")},
			arch:    "arm64",
			wantErr: ctypes.ErrInsufficientCapacity,
		},
		{
			name:    "named arch other than the single node's is refused",
			nodes:   inventoryV1.Nodes{archNode("amd", 1000, "amd64")},
			arch:    "arm64",
			wantErr: ctypes.ErrInsufficientCapacity,
		},
		{
			name:          "node that reports no arch takes a named arch",
			nodes:         inventoryV1.Nodes{archNode("unknown", 1000, "")},
			arch:          "arm64",
			wantAllocated: []int64{500},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replicas := tt.replicas
			if replicas == 0 {
				replicas = 1
			}

			inv := newInventory(inventoryV1.Cluster{Nodes: tt.nodes})

			err := inv.Adjust(archReservation(500, tt.arch, replicas))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			snapshot := inv.Snapshot()
			require.Len(t, snapshot.Nodes, len(tt.wantAllocated))
			for i, want := range tt.wantAllocated {
				require.Equal(t, want, snapshot.Nodes[i].Resources.CPU.Quantity.Allocated.MilliValue(), "node %s", snapshot.Nodes[i].Name)
			}
		})
	}
}
