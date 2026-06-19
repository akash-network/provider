package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"

	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

func TestResourceRequiresInterconnect(t *testing.T) {
	tests := []struct {
		name string
		res  rtypes.Resources
		want bool
	}{
		{
			name: "nil GPU never requires interconnect",
			res:  rtypes.Resources{},
			want: false,
		},
		{
			name: "GPU without interconnect/group attribute does not require",
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
			name: "GPU with implicit interconnect/group=auto requires interconnect",
			res: rtypes.Resources{
				GPU: &rtypes.GPU{
					Units: rtypes.NewResourceValue(8),
					Attributes: attrtypes.Attributes{
						{Key: "vendor/nvidia/model/a100", Value: "true"},
						{Key: AttributeGPUInterconnectGroupKey, Value: "auto"},
					},
				},
			},
			want: true,
		},
		{
			name: "GPU with explicit interconnect/group=pair0 requires interconnect",
			res: rtypes.Resources{
				GPU: &rtypes.GPU{
					Units: rtypes.NewResourceValue(8),
					Attributes: attrtypes.Attributes{
						{Key: AttributeGPUInterconnectGroupKey, Value: "pair0"},
					},
				},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ResourceRequiresInterconnect(tc.res))
		})
	}
}

// interconnectGroupSpec returns a representative GroupSpec carrying the placement
// attributes the test rows want.
func interconnectGroupSpec(attrs attrtypes.Attributes) dvbeta.GroupSpec {
	return dvbeta.GroupSpec{
		Name: "interconnect",
		Requirements: attrtypes.PlacementRequirements{
			Attributes: attrs,
		},
	}
}

// Spec calls out that PlacementRequiredFabric must work across every
// concrete ResourceGroup the provider's bid path may produce. We pin all
// four explicitly here so a future refactor that drops a case fails loud.
func TestPlacementRequiredFabric_AllConcreteTypes(t *testing.T) {
	ibSpec := interconnectGroupSpec(attrtypes.Attributes{
		{Key: AttributeInterconnectPlacement, Value: "true"},
		{Key: AttributeInterconnectFabricInfiniBand, Value: "true"},
	})
	roceSpec := interconnectGroupSpec(attrtypes.Attributes{
		{Key: AttributeInterconnectPlacement, Value: "true"},
		{Key: AttributeInterconnectFabricRoCE, Value: "true"},
	})
	noPinSpec := interconnectGroupSpec(attrtypes.Attributes{
		{Key: AttributeInterconnectPlacement, Value: "true"},
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
		// No fabric pin (just capabilities/gpu-interconnect=true) reports no fabric.
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

func TestPlacementRequiresInterconnect(t *testing.T) {
	withInterconnect := interconnectGroupSpec(attrtypes.Attributes{
		{Key: AttributeInterconnectPlacement, Value: "true"},
	})
	withoutinterconnect := interconnectGroupSpec(attrtypes.Attributes{
		{Key: "capabilities/gpu/vendor/nvidia", Value: "true"},
	})
	interconnectFalse := interconnectGroupSpec(attrtypes.Attributes{
		{Key: AttributeInterconnectPlacement, Value: "false"},
	})

	require.True(t, PlacementRequiresInterconnect(&dvbeta.Group{GroupSpec: withInterconnect}))
	require.True(t, PlacementRequiresInterconnect(withInterconnect))
	require.True(t, PlacementRequiresInterconnect(&withInterconnect))

	require.False(t, PlacementRequiresInterconnect(&dvbeta.Group{GroupSpec: withoutinterconnect}))
	require.False(t, PlacementRequiresInterconnect(withoutinterconnect))
	require.False(t, PlacementRequiresInterconnect(interconnectFalse))
}
