package inventory

import (
	"os"
	"path/filepath"
	"strings"
)

// InfinibandSysfsPath is where the operator's hardware-discovery DaemonSet
// pod mounts the host's `/sys/class/infiniband` directory. Each entry under
// it is a symlink to a Mellanox/NVIDIA HCA device (typically `mlx5_0`,
// `mlx5_1`, ...).
const InfinibandSysfsPath = "/host/sys/class/infiniband"

// IBDiscovery is the JSON payload the psutil HTTP server returns for
// `/infiniband`. Both fields are empty when the host has no IB devices or
// when the DaemonSet pod was started without the sysfs mount.
type IBDiscovery struct {
	// NCCLIBHCAPrefix is the common device-name prefix under
	// /sys/class/infiniband — `mlx5` for ConnectX-6/7. Empty when no
	// devices are present or when device names do not share a prefix.
	// Surfaced into the provider as NodeCapabilities.NCCLHCAPrefix and
	// injected by the workload builder as NCCL_IB_HCA.
	NCCLIBHCAPrefix string `json:"nccl_ib_hca_prefix"`

	// Fabric is "infiniband" or "roce", read from the link_layer of port 1
	// on the first detected device. Empty when no devices are present.
	// Surfaced into the provider as NodeCapabilities.InterconnectFabric and used
	// by the inventory client to gate fabric-pinned bids.
	Fabric string `json:"fabric"`
}

// DiscoverInfinibandFromSysfs walks `/sys/class/infiniband` on the host
// (via the InfinibandSysfsPath mount) and produces the IBDiscovery payload.
// Returns a zero-value IBDiscovery when sysfs is absent or unreadable; that
// is the natural state for non-interconnect nodes and the caller treats it as
// "this node has no interconnect capability." Errors are reserved for unexpected
// I/O conditions.
func DiscoverInfinibandFromSysfs() IBDiscovery {
	return discoverInfinibandAt(InfinibandSysfsPath)
}

// discoverInfinibandAt is the testable seam: callers pass an explicit
// directory so the unit tests can populate a fixture without needing
// real `/host/sys` mounts.
func discoverInfinibandAt(root string) IBDiscovery {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) == 0 {
		return IBDiscovery{}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return IBDiscovery{
		NCCLIBHCAPrefix: nccLHCAPrefixOf(names),
		Fabric:          fabricOfFirstHCA(root, names),
	}
}

// nccLHCAPrefixOf returns the HCA family prefix shared by every device
// name. For ConnectX-6/7 devices named `mlx5_0, mlx5_1, mlx5_2` this is
// `mlx5` (5 is the HCA family, the `_<n>` suffix is the instance index).
// We stop at the family/instance separator (`_` or `.`) so NCCL receives
// the family prefix it matches `NCCL_IB_HCA` against. Returns the empty
// string when the inputs do not share a family prefix.
func nccLHCAPrefixOf(names []string) string {
	if len(names) == 0 {
		return ""
	}

	prefix := familyPrefix(names[0])
	for _, n := range names[1:] {
		other := familyPrefix(n)
		i := 0
		for i < len(prefix) && i < len(other) && prefix[i] == other[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// familyPrefix returns the HCA family portion of a device name — everything
// up to (but not including) the first family/instance separator. Real-world
// Mellanox HCAs use `_` (`mlx5_0`); we also treat `.` defensively in case
// some future vendor uses a different separator.
func familyPrefix(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '_' || s[i] == '.' {
			return s[:i]
		}
	}
	return s
}

// fabricOfFirstHCA reads `<root>/<first device>/ports/1/link_layer` and
// translates the result into the provider's fabric string. Returns the
// empty string when no port reports a layer. A node with mixed link
// layers across devices is rare in practice; the operator runbook warns
// against it and this function honestly reports only the first device's
// port 1.
func fabricOfFirstHCA(root string, names []string) string {
	if len(names) == 0 {
		return ""
	}

	path := filepath.Join(root, names[0], "ports", "1", "link_layer")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	switch strings.TrimSpace(strings.ToLower(string(raw))) {
	case "infiniband":
		return "infiniband"
	case "ethernet":
		return "roce"
	}
	return ""
}
