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

type archTestReservation struct {
	group     dvbeta.GroupSpec
	allocated dvbeta.ResourceUnits
	params    interface{}
}

var _ ctypes.ReservationGroup = (*archTestReservation)(nil)

func (r *archTestReservation) Resources() dvbeta.ResourceGroup              { return r.group }
func (r *archTestReservation) SetAllocatedResources(v dvbeta.ResourceUnits) { r.allocated = v }
func (r *archTestReservation) GetAllocatedResources() dvbeta.ResourceUnits  { return r.allocated }
func (r *archTestReservation) SetClusterParams(v interface{})               { r.params = v }
func (r *archTestReservation) ClusterParams() interface{}                   { return r.params }

// archCluster is a single node with 1000m CPU whose inventory reports arch.
func archCluster(arch string) inventoryV1.Cluster {
	return inventoryV1.Cluster{
		Nodes: inventoryV1.Nodes{
			{
				Name: "node",
				Resources: inventoryV1.NodeResources{
					CPU: inventoryV1.CPU{
						Quantity: inventoryV1.NewResourcePairMilli(1000, 1000, 0, resource.DecimalSI),
						Info:     inventoryV1.CPUInfoS{{ID: "0", Vcores: 1, Arch: arch}},
					},
					Memory:           inventoryV1.Memory{Quantity: inventoryV1.NewResourcePair(1024, 1024, 0, resource.DecimalSI)},
					GPU:              inventoryV1.GPU{Quantity: inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI)},
					EphemeralStorage: inventoryV1.NewResourcePair(100, 100, 0, resource.DecimalSI),
					VolumesAttached:  inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
					VolumesMounted:   inventoryV1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				},
			},
		},
	}
}

// archTestOrder asks for one replica of 500m CPU whose SDL names arch, or names no arch
// when arch is empty.
func archTestOrder(arch string) *archTestReservation {
	cpu := &rtypes.CPU{Units: rtypes.NewResourceValue(500)}
	if arch != "" {
		cpu.Attributes = attrtypes.Attributes{{Key: "arch", Value: arch}}
	}

	return &archTestReservation{
		group: dvbeta.GroupSpec{
			Name: "group",
			Resources: dvbeta.ResourceUnits{
				{
					Resources: rtypes.Resources{
						ID:     1,
						CPU:    cpu,
						GPU:    &rtypes.GPU{Units: rtypes.NewResourceValue(0)},
						Memory: &rtypes.Memory{Quantity: rtypes.NewResourceValue(1)},
					},
					Count: 1,
				},
			},
		},
	}
}

func TestCPUArchMatches(t *testing.T) {
	arch := func(v string) attrtypes.Attributes {
		return attrtypes.Attributes{{Key: "arch", Value: v}}
	}
	node := func(v string) inventoryV1.CPUInfoS {
		return inventoryV1.CPUInfoS{{ID: "0", Vcores: 1, Arch: v}}
	}

	tests := []struct {
		name    string
		info    inventoryV1.CPUInfoS
		attrs   attrtypes.Attributes
		want    bool
		wantErr bool
	}{
		{name: "no arch named, arm64 node", info: node("arm64"), want: true},
		{name: "no arch named, amd64 node", info: node("amd64"), want: true},
		{name: "no arch named, node reports no arch", info: node(""), want: true},
		{name: "no arch named, node without cpu info", info: nil, want: true},
		{name: "no arch named, empty attributes", info: node("arm64"), attrs: attrtypes.Attributes{}, want: true},
		{name: "arm64 named, arm64 node", info: node("arm64"), attrs: arch("arm64"), want: true},
		{name: "arm64 named, amd64 node", info: node("amd64"), attrs: arch("arm64"), want: false},
		{name: "amd64 named, amd64 node", info: node("amd64"), attrs: arch("amd64"), want: true},
		{name: "amd64 named, arm64 node", info: node("arm64"), attrs: arch("amd64"), want: false},
		{name: "arm64 named, node reports no arch", info: node(""), attrs: arch("arm64"), want: true},
		{name: "arm64 named, node without cpu info", info: nil, attrs: arch("arm64"), want: true},
		{name: "unsupported arch", info: node("arm64"), attrs: arch("sparc64"), wantErr: true},
		{name: "unsupported cpu attribute", info: node("arm64"), attrs: attrtypes.Attributes{{Key: "vendor", Value: "intel"}}, wantErr: true},
		{name: "unsupported arch, node reports no arch", info: node(""), attrs: arch("sparc64"), wantErr: true},
		{name: "unsupported cpu attribute, node without cpu info", info: nil, attrs: attrtypes.Attributes{{Key: "vendor", Value: "intel"}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CPUArchMatches(tt.info, tt.attrs)
			if tt.wantErr {
				require.Error(t, err)
				require.False(t, got)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// The null inventory applies the same arch rule as the kube one: an SDL without arch
// goes to a node of any arch, a named arch narrows.
func TestNullInventoryAdjustCPUArch(t *testing.T) {
	t.Run("single arm64 node takes an SDL without arch", func(t *testing.T) {
		inv := newInventory(archCluster("arm64"))

		require.NoError(t, inv.Adjust(archTestOrder("")))
		require.Equal(t, int64(500), inv.Snapshot().Nodes[0].Resources.CPU.Quantity.Allocated.MilliValue())
	})

	t.Run("named arch other than the single node's is refused", func(t *testing.T) {
		inv := newInventory(archCluster("amd64"))

		require.ErrorIs(t, inv.Adjust(archTestOrder("arm64")), ctypes.ErrInsufficientCapacity)
	})
}
