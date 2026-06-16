package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeIBNode constructs a fixture mirroring `/sys/class/infiniband` on a
// real node: each device is a directory (symlink target in production)
// with `ports/1/link_layer` underneath.
func fakeIBNode(t *testing.T, devices map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for dev, layer := range devices {
		dir := filepath.Join(root, dev, "ports", "1")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		if layer != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "link_layer"), []byte(layer+"\n"), 0o644))
		}
	}
	return root
}

func TestDiscoverInfiniband_InfiniBand8Port(t *testing.T) {
	// A representative 8-HCA ConnectX-6 InfiniBand node: every device
	// reports `InfiniBand` on port 1.
	root := fakeIBNode(t, map[string]string{
		"mlx5_0": "InfiniBand",
		"mlx5_1": "InfiniBand",
		"mlx5_2": "InfiniBand",
		"mlx5_3": "InfiniBand",
		"mlx5_4": "InfiniBand",
		"mlx5_5": "InfiniBand",
		"mlx5_6": "InfiniBand",
		"mlx5_7": "InfiniBand",
	})

	got := discoverInfinibandAt(root)
	require.Equal(t, []string{"mlx5"}, got.NCCLIBHCAPrefixes)
	require.Equal(t, "infiniband", got.Fabric)
}

func TestDiscoverInfiniband_RoCE(t *testing.T) {
	// A RoCE node: same HCA family, link layer reports Ethernet.
	root := fakeIBNode(t, map[string]string{
		"mlx5_0": "Ethernet",
		"mlx5_1": "Ethernet",
	})

	got := discoverInfinibandAt(root)
	require.Equal(t, []string{"mlx5"}, got.NCCLIBHCAPrefixes)
	require.Equal(t, "roce", got.Fabric)
}

// AKT-492: mixed-vendor hosts publish every distinct HCA family rather than
// reducing to a shared character prefix. NCCL accepts a comma-separated
// list directly, so the workload builder can plumb both families through.
func TestDiscoverInfiniband_MixedVendor(t *testing.T) {
	root := fakeIBNode(t, map[string]string{
		"mlx5_0":   "InfiniBand",
		"mlx5_1":   "InfiniBand",
		"bnxt_re0": "InfiniBand",
		"bnxt_re1": "InfiniBand",
	})

	got := discoverInfinibandAt(root)
	require.ElementsMatch(t, []string{"mlx5", "bnxt_re"}, got.NCCLIBHCAPrefixes)
	require.Equal(t, "infiniband", got.Fabric)
}

func TestDiscoverInfiniband_NoDevices(t *testing.T) {
	// A node without any HCAs: an empty sysfs directory yields a
	// zero-value IBDiscovery, signalling "no RDMA capability."
	root := t.TempDir()

	got := discoverInfinibandAt(root)
	require.Empty(t, got.NCCLIBHCAPrefixes)
	require.Empty(t, got.Fabric)
}

func TestDiscoverInfiniband_MissingPath(t *testing.T) {
	// Pointing at a path that doesn't exist (the common case on
	// non-interconnect providers) returns zero values without error.
	got := discoverInfinibandAt("/nonexistent/path/that/does/not/exist")
	require.Empty(t, got.NCCLIBHCAPrefixes)
	require.Empty(t, got.Fabric)
}

func TestDiscoverInfiniband_UnknownLinkLayer(t *testing.T) {
	// Anything other than InfiniBand / Ethernet (e.g. a future
	// fabric type, or a misconfigured device) reports an empty
	// fabric so the provider declines to advertise a guess.
	root := fakeIBNode(t, map[string]string{
		"mlx5_0": "Future-Fabric-7",
	})

	got := discoverInfinibandAt(root)
	require.Equal(t, []string{"mlx5"}, got.NCCLIBHCAPrefixes, "prefix is still derivable")
	require.Empty(t, got.Fabric, "unknown link_layer reports empty fabric")
}

func TestDiscoverInfiniband_MissingLinkLayerFile(t *testing.T) {
	// A device with no ports/1/link_layer file (rare but possible
	// transient state) reports empty fabric, never panics.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mlx5_0", "ports", "1"), 0o755))

	got := discoverInfinibandAt(root)
	require.Equal(t, []string{"mlx5"}, got.NCCLIBHCAPrefixes)
	require.Empty(t, got.Fabric)
}

func TestNCCLHCAPrefixesOf(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		// Real /sys/class/infiniband entries always carry an instance
		// index (`mlx5_0`, `bnxt_re0`, etc.) — bare family-name entries
		// don't exist in production, so we don't test that shape.
		{"single mlx5_0", []string{"mlx5_0"}, []string{"mlx5"}},
		{"all mlx5", []string{"mlx5_0", "mlx5_1", "mlx5_7"}, []string{"mlx5"}},
		{"two distinct families with separator-style indexes", []string{"mlx5_0", "bnxt_re0"}, []string{"mlx5", "bnxt_re"}},
		{"legacy ib<n> naming strips trailing digit", []string{"ib0", "ib1"}, []string{"ib"}},
		{"empty input", []string{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, nccLHCAPrefixesOf(tc.input))
		})
	}
}
