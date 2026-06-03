package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"

	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

func TestResourceRequiresRDMA(t *testing.T) {
	tests := []struct {
		name string
		res  rtypes.Resources
		want bool
	}{
		{
			name: "nil GPU never requires RDMA",
			res:  rtypes.Resources{},
			want: false,
		},
		{
			name: "GPU without rdma=true attribute does not require",
			res: rtypes.Resources{
				GPU: &rtypes.GPU{
					Units: rtypes.NewResourceValue(8),
					Attributes: attrtypes.Attributes{
						{Key: "vendor/nvidia/model/a100", Value: "true"},
					},
				},
			},
			want: false,
		},
		{
			name: "GPU with rdma=true requires RDMA",
			res: rtypes.Resources{
				GPU: &rtypes.GPU{
					Units: rtypes.NewResourceValue(8),
					Attributes: attrtypes.Attributes{
						{Key: "vendor/nvidia/model/a100", Value: "true"},
						{Key: AttributeGPURDMAKey, Value: "true"},
					},
				},
			},
			want: true,
		},
		{
			name: "GPU with rdma=false does not require",
			res: rtypes.Resources{
				GPU: &rtypes.GPU{
					Units: rtypes.NewResourceValue(8),
					Attributes: attrtypes.Attributes{
						{Key: AttributeGPURDMAKey, Value: "false"},
					},
				},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ResourceRequiresRDMA(tc.res))
		})
	}
}

// rdmaGroupSpec returns a representative GroupSpec carrying the placement
// attributes the test rows want.
func rdmaGroupSpec(attrs attrtypes.Attributes) dvbeta.GroupSpec {
	return dvbeta.GroupSpec{
		Name: "rdma",
		Requirements: attrtypes.PlacementRequirements{
			Attributes: attrs,
		},
	}
}

// Spec calls out that PlacementRequiredFabric must work across every
// concrete ResourceGroup the provider's bid path may produce. We pin all
// four explicitly here so a future refactor that drops a case fails loud.
func TestPlacementRequiredFabric_AllConcreteTypes(t *testing.T) {
	ibSpec := rdmaGroupSpec(attrtypes.Attributes{
		{Key: AttributeRDMAPlacement, Value: "true"},
		{Key: AttributeRDMAFabricInfiniBand, Value: "true"},
	})
	roceSpec := rdmaGroupSpec(attrtypes.Attributes{
		{Key: AttributeRDMAPlacement, Value: "true"},
		{Key: AttributeRDMAFabricRoCE, Value: "true"},
	})
	noPinSpec := rdmaGroupSpec(attrtypes.Attributes{
		{Key: AttributeRDMAPlacement, Value: "true"},
	})

	type tc struct {
		name        string
		input       dvbeta.ResourceGroup
		wantFabric  string
		wantPresent bool
	}
	rows := []tc{
		// IB across all four concrete types.
		{"*Group ib", &dvbeta.Group{GroupSpec: ibSpec}, "infiniband", true},
		{"Group ib", dvbeta.Group{GroupSpec: ibSpec}, "infiniband", true},
		{"*GroupSpec ib", &ibSpec, "infiniband", true},
		{"GroupSpec ib", ibSpec, "infiniband", true},
		// RoCE across all four concrete types.
		{"*Group roce", &dvbeta.Group{GroupSpec: roceSpec}, "roce", true},
		{"Group roce", dvbeta.Group{GroupSpec: roceSpec}, "roce", true},
		{"*GroupSpec roce", &roceSpec, "roce", true},
		{"GroupSpec roce", roceSpec, "roce", true},
		// No fabric pin (just capabilities/rdma=true) reports no fabric.
		{"*Group no-pin", &dvbeta.Group{GroupSpec: noPinSpec}, "", false},
		{"GroupSpec no-pin", noPinSpec, "", false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			fabric, ok := PlacementRequiredFabric(r.input)
			require.Equal(t, r.wantPresent, ok)
			require.Equal(t, r.wantFabric, fabric)
		})
	}
}

func TestPlacementRequiresRDMA(t *testing.T) {
	withRDMA := rdmaGroupSpec(attrtypes.Attributes{
		{Key: AttributeRDMAPlacement, Value: "true"},
	})
	withoutRDMA := rdmaGroupSpec(attrtypes.Attributes{
		{Key: "capabilities/gpu/vendor/nvidia", Value: "true"},
	})
	rdmaFalse := rdmaGroupSpec(attrtypes.Attributes{
		{Key: AttributeRDMAPlacement, Value: "false"},
	})

	require.True(t, PlacementRequiresRDMA(&dvbeta.Group{GroupSpec: withRDMA}))
	require.True(t, PlacementRequiresRDMA(withRDMA))
	require.True(t, PlacementRequiresRDMA(&withRDMA))

	require.False(t, PlacementRequiresRDMA(&dvbeta.Group{GroupSpec: withoutRDMA}))
	require.False(t, PlacementRequiresRDMA(withoutRDMA))
	require.False(t, PlacementRequiresRDMA(rdmaFalse))
}
