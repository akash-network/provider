package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
