package inventory

import (
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
)

// Provider attribute keys for the GPU interconnect feature. The provider operator
// places these on `provider.yaml`; the inventory client consumes them when
// matching tenant placement requirements and when satisfying a per-resource
// interconnect opt-in. The values follow chain SDK convention (see
// pkg.akt.dev/go/sdl/v2 GPU attribute encoding for `gpu.attributes.interconnect`).
const (
	// AttributeInterconnectPlacement is the deployment-group placement attribute a
	// tenant adds to steer their workload to an interconnect-capable provider.
	// Provider opt-in is identical: the provider advertises this key on
	// its on-chain attributes when it has at least one node with interconnect
	// capacity.
	AttributeInterconnectPlacement = "capabilities/gpu-interconnect"

	// AttributeInterconnectFabricInfiniBand and AttributeInterconnectFabricRoCE are the
	// optional fabric pins. A tenant that requires only one fabric adds
	// the matching key; a provider advertises whichever fabric it actually
	// has. Matching is exact string equality.
	AttributeInterconnectFabricInfiniBand = "capabilities/gpu-interconnect/fabric/infiniband"
	AttributeInterconnectFabricRoCE       = "capabilities/gpu-interconnect/fabric/roce"

	// AttributeGPUInterconnectKey is the per-resource GPU attribute the chain SDK
	// emits when an SDL profile sets `gpu.attributes.interconnect: true`. The
	// inventory client reads it off `Resources.GPU.Attributes` to decide
	// whether to allocate an interconnect HCA for that resource.
	AttributeGPUInterconnectKey = "interconnect"

	// AttributeGPUInterconnectGroupKey is the per-resource GPU attribute the chain
	// SDK emits when an SDL profile sets `gpu.attributes.interconnect_group: <name>`.
	// Carries the peer-group label end-to-end so the bid engine's Adjust
	// step can enforce per-group node separation (AKT-443). The same value
	// is also surfaced on the off-chain Service.InterconnectGroup so the workload
	// builder can label pods for anti-affinity.
	AttributeGPUInterconnectGroupKey = "interconnect_group"

	// attributeTrueValue is the canonical "set" value emitted by the SDL
	// parser and matched here.
	attributeTrueValue = "true"
)

// ResourceRequiresInterconnect reports whether a per-service resource has opted
// into interconnect via `gpu.attributes.interconnect: true`. This is the per-service signal
// the provider's reservation logic gates `tryAdjustInterconnect` on; it is
// independent of (and complementary to) the deployment-group placement
// attribute, which steers bid acceptance.
func ResourceRequiresInterconnect(res rtypes.Resources) bool {
	if res.GPU == nil {
		return false
	}
	for _, a := range res.GPU.Attributes {
		if a.Key == AttributeGPUInterconnectKey && a.Value == attributeTrueValue {
			return true
		}
	}
	return false
}

// ResourceInterconnectGroup returns the peer-group label declared on this
// per-service resource, or "" if none. Drives the bid engine's per-group
// node-separation tracking in Adjust: every resource carrying the same
// non-empty group must land on a distinct node. The chain SDK serializes
// this attribute end-to-end (see chain-sdk go/sdl/gpu.go for the emit
// site and the matching exported key constant `GPUAttributeInterconnectGroup`).
func ResourceInterconnectGroup(res rtypes.Resources) string {
	if res.GPU == nil {
		return ""
	}
	for _, a := range res.GPU.Attributes {
		if a.Key == AttributeGPUInterconnectGroupKey {
			return a.Value
		}
	}
	return ""
}

// PlacementRequiredFabric reports whether a deployment-group's placement
// requirements pin interconnect to a specific fabric. Returns
// (fabric, true) when one of the two recognized fabric keys is required,
// where fabric is `"infiniband"` or `"roce"` and matches NodeCapabilities.
// InterconnectFabric exactly. Returns ("", false) when no fabric is pinned, in
// which case the provider may satisfy the bid from any interconnect-capable node.
//
// rgroup is typed as the deployment SDK's ResourceGroup interface so the
// helper handles every concrete shape the provider's commit path produces
// (`*Group`, `Group`, `*GroupSpec`, `GroupSpec`). A future concrete type
// would land in the default arm and report no pin — a safe, permissive
// degradation. CS-6 in the chain SDK pins `Requirements` preservation in
// place so the helper never sees a stripped slice.
func PlacementRequiredFabric(rgroup dvbeta.ResourceGroup) (string, bool) {
	switch rg := rgroup.(type) {
	case *dvbeta.Group:
		return placementFabricFromAttrs(rg.GroupSpec.Requirements.Attributes)
	case dvbeta.Group:
		return placementFabricFromAttrs(rg.GroupSpec.Requirements.Attributes)
	case *dvbeta.GroupSpec:
		return placementFabricFromAttrs(rg.Requirements.Attributes)
	case dvbeta.GroupSpec:
		return placementFabricFromAttrs(rg.Requirements.Attributes)
	default:
		return "", false
	}
}

// PlacementRequiresInterconnect reports whether the deployment-group's placement
// requirements include `capabilities/gpu-interconnect=true`. Used by the bid engine to
// decide whether a non-interconnect provider can serve the order at all.
func PlacementRequiresInterconnect(rgroup dvbeta.ResourceGroup) bool {
	switch rg := rgroup.(type) {
	case *dvbeta.Group:
		return placementHasinterconnect(rg.GroupSpec.Requirements.Attributes)
	case dvbeta.Group:
		return placementHasinterconnect(rg.GroupSpec.Requirements.Attributes)
	case *dvbeta.GroupSpec:
		return placementHasinterconnect(rg.Requirements.Attributes)
	case dvbeta.GroupSpec:
		return placementHasinterconnect(rg.Requirements.Attributes)
	default:
		return false
	}
}

func placementFabricFromAttrs(attrs attrtypes.Attributes) (string, bool) {
	for _, a := range attrs {
		if a.Value != attributeTrueValue {
			continue
		}
		switch a.Key {
		case AttributeInterconnectFabricInfiniBand:
			return "infiniband", true
		case AttributeInterconnectFabricRoCE:
			return "roce", true
		}
	}
	return "", false
}

func placementHasinterconnect(attrs attrtypes.Attributes) bool {
	for _, a := range attrs {
		if a.Key == AttributeInterconnectPlacement && a.Value == attributeTrueValue {
			return true
		}
	}
	return false
}
