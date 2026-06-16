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
	// NCCLIBHCAPrefixes is every distinct HCA device-name family present
	// under /sys/class/infiniband — e.g. ["mlx5"] on a uniform Mellanox
	// host or ["mlx5","bnxt_re"] on a mixed-vendor node. Empty when no
	// devices are present. Surfaced into the provider as
	// NodeCapabilities.NCCLHCAPrefixes and joined with `,` by the
	// workload builder as NCCL_IB_HCA (NCCL accepts comma-separated
	// device prefixes natively).
	NCCLIBHCAPrefixes []string `json:"nccl_ib_hca_prefixes"`

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
		NCCLIBHCAPrefixes: nccLHCAPrefixesOf(names),
		Fabric:            fabricOfFirstHCA(root, names),
	}
}

// nccLHCAPrefixesOf returns every distinct HCA family prefix among the
// device names. For ConnectX-6/7 devices named `mlx5_0, mlx5_1, mlx5_2`
// this is ["mlx5"]; on a mixed-vendor host with `mlx5_0, bnxt_re0` it
// returns ["mlx5","bnxt_re"]. We stop at the family/instance separator
// (`_` or `.`) so NCCL receives the family prefix it matches
// `NCCL_IB_HCA` against. Returns nil when the input is empty.
// Output order is deterministic (first-seen order in the input slice).
func nccLHCAPrefixesOf(names []string) []string {
	if len(names) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		p := familyPrefix(n)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// familyPrefix returns the HCA family portion of a device name by
// stripping the trailing instance index (and the optional `_` separator
// preceding it). Examples:
//
//   - `mlx5_0`    → `mlx5`     (Mellanox ConnectX, `_<n>` instance)
//   - `bnxt_re0`  → `bnxt_re`  (Broadcom RoCE, no separator before index)
//   - `ib0`       → `ib`       (legacy generic IB interface)
//   - `mlx5`      → `mlx5`     (no instance suffix; unchanged)
//
// Vendors don't share a universal naming convention beyond
// `<family><instance-digits>` — splitting on `_` would incorrectly
// chop `bnxt_re` down to `bnxt` and conflate it with `bnxt_en`. So we
// trim from the right: drop the digit run, then optionally drop a
// single `_` separator if one sits between the digits and the family.
func familyPrefix(s string) string {
	end := len(s)
	for end > 0 && s[end-1] >= '0' && s[end-1] <= '9' {
		end--
	}
	if end == len(s) {
		// No trailing digits — name is already the family (or has no instance index).
		return s
	}
	if end > 0 && s[end-1] == '_' {
		end--
	}
	return s[:end]
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
