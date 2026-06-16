package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "pkg.akt.dev/go/inventory/v1"
)

// AKT-493: matchInterconnectResourceName should keep working unchanged for
// providers running the Mellanox/NVIDIA k8s-rdma-shared-device-plugin
// (the rc4 hardcoded default), AND match operator-configured patterns
// for non-Mellanox plugins.

func resList(names ...string) corev1.ResourceList {
	out := corev1.ResourceList{}
	for _, n := range names {
		out[corev1.ResourceName(n)] = *resource.NewQuantity(1, resource.DecimalSI)
	}
	return out
}

func TestMatchInterconnectResourceName_DefaultMellanoxPrefix(t *testing.T) {
	// rc4 contract: empty patterns → fall back to the Mellanox/NVIDIA
	// prefix so existing providers keep working without touching config.
	got := matchInterconnectResourceName(
		resList("cpu", "memory", "rdma/rdma_shared_device_ib"),
		nil,
	)
	require.Equal(t, corev1.ResourceName("rdma/rdma_shared_device_ib"), got)
}

func TestMatchInterconnectResourceName_DefaultMatchesRoCEVariant(t *testing.T) {
	got := matchInterconnectResourceName(
		resList("cpu", "rdma/rdma_shared_device_eth"),
		[]string{},
	)
	require.Equal(t, corev1.ResourceName("rdma/rdma_shared_device_eth"), got)
}

func TestMatchInterconnectResourceName_NoMatch(t *testing.T) {
	// A node with no interconnect-eligible resource returns empty so the
	// inventory client treats it as non-interconnect.
	got := matchInterconnectResourceName(
		resList("cpu", "memory", "nvidia.com/gpu"),
		nil,
	)
	require.Equal(t, corev1.ResourceName(""), got)
}

func TestMatchInterconnectResourceName_BarePatternIsPrefix(t *testing.T) {
	// A configured bare string with no glob metacharacter matches as a
	// prefix — that's the same semantics rc4 had, so providers who set
	// the exact prefix in config keep the same behavior.
	got := matchInterconnectResourceName(
		resList("cpu", "broadcom.com/rdma_0"),
		[]string{"broadcom.com/rdma"},
	)
	require.Equal(t, corev1.ResourceName("broadcom.com/rdma_0"), got)
}

func TestMatchInterconnectResourceName_GlobPattern(t *testing.T) {
	got := matchInterconnectResourceName(
		resList("cpu", "intel.com/iwarp_shared_0"),
		[]string{"intel.com/iwarp_*"},
	)
	require.Equal(t, corev1.ResourceName("intel.com/iwarp_shared_0"), got)
}

func TestMatchInterconnectResourceName_MultiplePatternsFirstHitWins(t *testing.T) {
	// The matcher iterates `quantities` first (map order is unstable),
	// then patterns — so what we can pin here is "any of the configured
	// patterns matched a resource that's actually present." This test
	// uses a single eligible resource so the order is unambiguous.
	got := matchInterconnectResourceName(
		resList("cpu", "memory", "broadcom.com/rdma"),
		[]string{"intel.com/iwarp_*", "broadcom.com/rdma"},
	)
	require.Equal(t, corev1.ResourceName("broadcom.com/rdma"), got)
}

// CodeRabbit follow-up (PR #400 review 2026-06-15): when a node lists
// multiple eligible resources, the configured pattern order — not Go's
// random map iteration order — must decide which one wins. This pins
// the precedence so a future refactor that flips the loop order
// regresses loudly instead of silently.
func TestMatchInterconnectResourceName_FirstConfiguredPatternWins(t *testing.T) {
	quantities := resList("intel.com/iwarp_shared_0", "broadcom.com/rdma")

	gotIntelFirst := matchInterconnectResourceName(quantities, []string{"intel.com/iwarp_*", "broadcom.com/rdma"})
	require.Equal(t, corev1.ResourceName("intel.com/iwarp_shared_0"), gotIntelFirst,
		"intel pattern listed first must win even when broadcom is also present")

	gotBroadcomFirst := matchInterconnectResourceName(quantities, []string{"broadcom.com/rdma", "intel.com/iwarp_*"})
	require.Equal(t, corev1.ResourceName("broadcom.com/rdma"), gotBroadcomFirst,
		"reversing the pattern order must reverse the resource picked")
}

// CodeRabbit follow-up (PR #400 review 2026-06-15): updateNodeInfo must
// clear stale interconnect fields when the kubelet no longer publishes
// the matching extended resource. Without the reset, a node that loses
// its RDMA device plugin (operator restart, plugin uninstall, pattern
// config change) retains the previously-set InterconnectResourceName
// and GPUInterconnect.{Capacity,Allocatable} indefinitely.
func TestUpdateNodeInfo_ClearsStaleInterconnectWhenResourceGone(t *testing.T) {
	// Start with a node that already has interconnect state populated —
	// this mirrors the state after a previous updateNodeInfo run on a
	// node that was advertising rdma/rdma_shared_device_ib.
	node := v1.Node{
		Resources: v1.NodeResources{
			CPU:              v1.CPU{Quantity: v1.NewResourcePairMilli(0, 0, 0, resource.DecimalSI)},
			GPU:              v1.GPU{Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI)},
			Memory:           v1.Memory{Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI)},
			EphemeralStorage: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesAttached:  v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesMounted:   v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			GPUInterconnect:  v1.NewResourcePair(8, 8, 0, resource.DecimalSI),
		},
		Capabilities: v1.NodeCapabilities{
			InterconnectResourceName: "rdma/rdma_shared_device_ib",
		},
	}

	// kubelet no longer publishes any interconnect-eligible resource.
	knode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
		Status: corev1.NodeStatus{
			Allocatable: resList("cpu", "memory"),
			Capacity:    resList("cpu", "memory"),
		},
	}

	updateNodeInfo(knode, &node, nil)

	require.Empty(t, node.Capabilities.InterconnectResourceName,
		"InterconnectResourceName must clear when the device plugin's resource is gone")
	require.Equal(t, int64(0), node.Resources.GPUInterconnect.Capacity.Value(),
		"GPUInterconnect.Capacity must reset to 0 when no resource matches")
	require.Equal(t, int64(0), node.Resources.GPUInterconnect.Allocatable.Value(),
		"GPUInterconnect.Allocatable must reset to 0 when no resource matches")
}

func TestMatchInterconnectResourceName_MalformedGlobIsNotAMatch(t *testing.T) {
	// `[` opens a character class; without a closing bracket
	// filepath.Match returns an error. We fail-closed (no match) so a
	// typo in provider.yaml doesn't accidentally pick the wrong
	// resource — the operator's inventory shows the node as
	// non-interconnect and the misconfiguration is visible.
	got := matchInterconnectResourceName(
		resList("rdma/rdma_shared_device_ib"),
		[]string{"rdma/["},
	)
	require.Equal(t, corev1.ResourceName(""), got)
}
