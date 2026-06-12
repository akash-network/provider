package hostprobe

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultSourcesCollectFromRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "/proc/cpuinfo", `processor   : 0
model name  : Intel(R) Xeon(R)
cpu cores   : 4
flags       : fpu sse

processor   : 1
model name  : Intel(R) Xeon(R)
cpu cores   : 4
flags       : fpu sse
`)
	writeFile(t, root, "/sys/devices/system/cpu/online", "0-1")
	writeFile(t, root, "/sys/class/dmi/id/sys_vendor", "Akash Test")
	writeFile(t, root, "/sys/class/dmi/id/product_name", "Bare Metal")
	writeFile(t, root, "/proc/meminfo", "MemTotal:       16384 kB\n")
	writeFile(t, root, "/sys/devices/system/edac/mc/mc0/dimm0/dimm_mem_size", "16")
	writeFile(t, root, "/sys/bus/pci/devices/0000:00:02.0/class", "0x030000")
	writeFile(t, root, "/sys/bus/pci/devices/0000:00:02.0/vendor", "0x10de")
	writeFile(t, root, "/sys/bus/pci/devices/0000:00:02.0/device", "0x1eb8")
	writeFile(t, root, "/sys/block/nvme0n1/size", "2048")
	writeFile(t, root, "/sys/class/net/eth0/speed", "10000")
	writeFile(t, root, "/proc/sys/kernel/osrelease", "6.1.0-akash")

	collector := NewCollector(Config{
		Root:     root,
		Hostname: func() (string, error) { return "host-a", nil },
	})

	snapshot, err := collector.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, "6.1.0-akash", snapshot.Host.KernelRelease)
	require.Len(t, snapshot.Sources, len(DefaultSources()))
	require.Empty(t, snapshot.Discrepancies)
	require.False(t, snapshot.Virtualization.Detected)

	byName := make(map[string]SourceResult)
	for _, source := range snapshot.Sources {
		byName[source.Name] = source
		require.Equal(t, SourceStatusOK, source.Status, source.Name)
	}

	require.Equal(t, "2", byName["proc.cpuinfo"].Properties["logical_cpus"])
	require.Equal(t, "2", byName["sysfs.cpu_topology"].Properties["logical_cpus"])
	require.Equal(t, "16777216", byName["proc.meminfo"].Properties["total_bytes"])
	require.Equal(t, "16777216", byName["sysfs.edac"].Properties["total_bytes"])
	require.Equal(t, "1", byName["sysfs.pci"].Properties["gpu_count"])
	require.Equal(t, "1048576", byName["sysfs.block"].Properties["total_bytes"])
	require.Equal(t, "10000", byName["sysfs.net"].Properties["max_speed_mbps"])
}

func TestCountCPUList(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{
			value: "0",
			want:  1,
		},
		{
			value: "0-3",
			want:  4,
		},
		{
			value: "0-3,8,10-11",
			want:  7,
		},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := countCPUList(test.value)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestDetectVirtualization(t *testing.T) {
	report := DetectVirtualization([]SourceResult{
		{
			Name:          "proc.cpuinfo",
			HardwareClass: hardwareClassCPU,
			Status:        SourceStatusOK,
			Properties: map[string]string{
				"flags": "fpu sse hypervisor",
			},
		},
		{
			Name:          "sysfs.dmi",
			HardwareClass: hardwareClassPlatform,
			Status:        SourceStatusOK,
			Properties: map[string]string{
				"product_name": "KVM",
			},
		},
		{
			Name:          "sysfs.pci",
			HardwareClass: hardwareClassGPU,
			Status:        SourceStatusOK,
			Properties: map[string]string{
				"virtual_devices": "0x1af4:0x1000",
			},
		},
	})

	require.True(t, report.Detected)
	require.Equal(t, "kvm,virtio", report.Hypervisor)
	require.Equal(t, []string{
		"cpuinfo_hypervisor_flag",
		"dmi_platform",
		"pci_virtual_device",
	}, report.Methods)
}

func writeFile(t *testing.T, root string, path string, data string) {
	t.Helper()

	fullPath := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(data), 0o600))
}
