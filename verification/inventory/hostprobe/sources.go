package hostprobe

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	hardwareClassCPU      = "cpu"
	hardwareClassGPU      = "gpu"
	hardwareClassMemory   = "memory"
	hardwareClassNetwork  = "network"
	hardwareClassPCI      = "pci"
	hardwareClassPlatform = "platform"
	hardwareClassStorage  = "storage"
)

type sourceFunc struct {
	name          string
	hardwareClass string
	method        string
	trustDomain   string
	collect       func(context.Context, FileSource) (map[string]string, error)
}

func (s sourceFunc) Name() string {
	return s.name
}

func (s sourceFunc) HardwareClass() string {
	return s.hardwareClass
}

func (s sourceFunc) Method() string {
	return s.method
}

func (s sourceFunc) TrustDomain() string {
	return s.trustDomain
}

func (s sourceFunc) Collect(ctx context.Context, fs FileSource) (map[string]string, error) {
	return s.collect(ctx, fs)
}

func DefaultSources() []Source {
	return []Source{
		procCPUInfoSource(),
		sysCPUTopologySource(),
		dmiPlatformSource(),
		procMemInfoSource(),
		edacMemorySource(),
		pciDeviceSource(),
		sysBlockSource(),
		sysNetworkSource(),
	}
}

func procCPUInfoSource() Source {
	return sourceFunc{
		name:          "proc.cpuinfo",
		hardwareClass: hardwareClassCPU,
		method:        "/proc/cpuinfo",
		trustDomain:   "kernel_procfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			data, err := fs.ReadFile("/proc/cpuinfo")
			if err != nil {
				return nil, err
			}

			return parseCPUInfo(string(data)), nil
		},
	}
}

func sysCPUTopologySource() Source {
	return sourceFunc{
		name:          "sysfs.cpu_topology",
		hardwareClass: hardwareClassCPU,
		method:        "/sys/devices/system/cpu/online",
		trustDomain:   "kernel_sysfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			online, err := fs.ReadString("/sys/devices/system/cpu/online")
			if err != nil {
				return nil, err
			}

			count, err := countCPUList(online)
			if err != nil {
				return nil, err
			}

			return map[string]string{
				"logical_cpus": strconv.Itoa(count),
			}, nil
		},
	}
}

func dmiPlatformSource() Source {
	return sourceFunc{
		name:          "sysfs.dmi",
		hardwareClass: hardwareClassPlatform,
		method:        "/sys/class/dmi/id",
		trustDomain:   "firmware_dmi",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			fields := map[string]string{
				"sys_vendor":      "/sys/class/dmi/id/sys_vendor",
				"product_name":    "/sys/class/dmi/id/product_name",
				"product_version": "/sys/class/dmi/id/product_version",
				"board_vendor":    "/sys/class/dmi/id/board_vendor",
			}

			properties := make(map[string]string)
			for key, path := range fields {
				value, err := fs.ReadString(path)
				if err == nil && value != "" {
					properties[key] = value
				}
			}

			if len(properties) == 0 {
				return nil, ErrUnavailable
			}

			return properties, nil
		},
	}
}

func procMemInfoSource() Source {
	return sourceFunc{
		name:          "proc.meminfo",
		hardwareClass: hardwareClassMemory,
		method:        "/proc/meminfo",
		trustDomain:   "kernel_procfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			data, err := fs.ReadFile("/proc/meminfo")
			if err != nil {
				return nil, err
			}

			totalBytes, err := parseMemTotalBytes(string(data))
			if err != nil {
				return nil, err
			}

			return map[string]string{
				"total_bytes": strconv.FormatUint(totalBytes, 10),
			}, nil
		},
	}
}

func edacMemorySource() Source {
	return sourceFunc{
		name:          "sysfs.edac",
		hardwareClass: hardwareClassMemory,
		method:        "/sys/devices/system/edac",
		trustDomain:   "kernel_edac",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			matches, err := fs.Glob("/sys/devices/system/edac/mc/mc*/dimm*/dimm_mem_size")
			if err != nil {
				return nil, err
			}

			var totalMiB uint64
			for _, path := range matches {
				value, err := fs.ReadString(path)
				if err != nil {
					continue
				}

				amount, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
				if err != nil {
					continue
				}

				totalMiB += amount
			}

			if totalMiB == 0 {
				return nil, ErrUnavailable
			}

			return map[string]string{
				"total_bytes": strconv.FormatUint(totalMiB*1024*1024, 10),
			}, nil
		},
	}
}

func pciDeviceSource() Source {
	return sourceFunc{
		name:          "sysfs.pci",
		hardwareClass: hardwareClassPCI,
		method:        "/sys/bus/pci/devices",
		trustDomain:   "kernel_pci_sysfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			matches, err := fs.Glob("/sys/bus/pci/devices/*/class")
			if err != nil {
				return nil, err
			}

			var (
				gpus       []string
				netDevices []string
				virt       []string
			)

			for _, classPath := range matches {
				deviceDir := filepath.Dir(classPath)
				classValue, err := fs.ReadString(classPath)
				if err != nil {
					continue
				}

				vendor := readOptional(fs, filepath.Join(deviceDir, "vendor"))
				device := readOptional(fs, filepath.Join(deviceDir, "device"))
				entry := strings.ToLower(strings.TrimSpace(vendor + ":" + device))

				if strings.HasPrefix(classValue, "0x03") && entry != ":" {
					gpus = append(gpus, entry)
				}

				if strings.HasPrefix(classValue, "0x02") && entry != ":" {
					netDevices = append(netDevices, entry)
				}

				if isVirtualPCIDevice(vendor, device) && entry != ":" {
					virt = append(virt, entry)
				}
			}

			sort.Strings(gpus)
			sort.Strings(netDevices)
			sort.Strings(virt)

			return map[string]string{
				"gpu_count":       strconv.Itoa(len(gpus)),
				"gpu_devices":     joinList(gpus),
				"network_devices": joinList(netDevices),
				"virtual_devices": joinList(virt),
			}, nil
		},
	}
}

func sysBlockSource() Source {
	return sourceFunc{
		name:          "sysfs.block",
		hardwareClass: hardwareClassStorage,
		method:        "/sys/block",
		trustDomain:   "kernel_block_sysfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			matches, err := fs.Glob("/sys/block/*/size")
			if err != nil {
				return nil, err
			}

			var (
				totalBytes uint64
				devices    []string
			)

			for _, sizePath := range matches {
				name := filepath.Base(filepath.Dir(sizePath))
				if skipBlockDevice(name) {
					continue
				}

				value, err := fs.ReadString(sizePath)
				if err != nil {
					continue
				}

				sectors, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					continue
				}

				totalBytes += sectors * 512
				devices = append(devices, name)
			}

			if len(devices) == 0 {
				return nil, ErrUnavailable
			}

			sort.Strings(devices)

			return map[string]string{
				"device_count": strconv.Itoa(len(devices)),
				"devices":      joinList(devices),
				"total_bytes":  strconv.FormatUint(totalBytes, 10),
			}, nil
		},
	}
}

func sysNetworkSource() Source {
	return sourceFunc{
		name:          "sysfs.net",
		hardwareClass: hardwareClassNetwork,
		method:        "/sys/class/net",
		trustDomain:   "kernel_net_sysfs",
		collect: func(ctx context.Context, fs FileSource) (map[string]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			matches, err := fs.Glob("/sys/class/net/*")
			if err != nil {
				return nil, err
			}

			var (
				ifaces     []string
				maxSpeedMb uint64
			)

			for _, ifacePath := range matches {
				name := filepath.Base(ifacePath)
				if name == "lo" {
					continue
				}

				ifaces = append(ifaces, name)
				speed, err := fs.ReadString(filepath.Join(ifacePath, "speed"))
				if err != nil {
					continue
				}

				parsed, err := strconv.ParseUint(speed, 10, 64)
				if err == nil && parsed > maxSpeedMb {
					maxSpeedMb = parsed
				}
			}

			if len(ifaces) == 0 {
				return nil, ErrUnavailable
			}

			sort.Strings(ifaces)

			return map[string]string{
				"interface_count": strconv.Itoa(len(ifaces)),
				"interfaces":      joinList(ifaces),
				"max_speed_mbps":  strconv.FormatUint(maxSpeedMb, 10),
			}, nil
		},
	}
}

func parseCPUInfo(data string) map[string]string {
	properties := make(map[string]string)
	logicalCPUs := 0

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		key, value, ok := splitProcLine(scanner.Text())
		if !ok {
			continue
		}

		switch key {
		case "processor":
			logicalCPUs++
		case "model name":
			setFirst(properties, "model_name", value)
		case "cpu cores":
			setFirst(properties, "cores_per_socket", value)
		case "flags":
			setFirst(properties, "flags", value)
		}
	}

	if logicalCPUs > 0 {
		properties["logical_cpus"] = strconv.Itoa(logicalCPUs)
	}

	return properties
}

func parseMemTotalBytes(data string) (uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		key, value, ok := splitProcLine(scanner.Text())
		if !ok || key != "MemTotal" {
			continue
		}

		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0, fmt.Errorf("%w: missing MemTotal value", ErrUnavailable)
		}

		totalKiB, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, err
		}

		return totalKiB * 1024, nil
	}

	return 0, fmt.Errorf("%w: MemTotal not found", ErrUnavailable)
}

func splitProcLine(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(key), strings.TrimSpace(value), true
}

func countCPUList(value string) (int, error) {
	total := 0
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if start, end, ok := strings.Cut(part, "-"); ok {
			first, err := strconv.Atoi(start)
			if err != nil {
				return 0, err
			}

			last, err := strconv.Atoi(end)
			if err != nil {
				return 0, err
			}

			if last < first {
				return 0, fmt.Errorf("invalid cpu range %q", part)
			}

			total += last - first + 1
			continue
		}

		if _, err := strconv.Atoi(part); err != nil {
			return 0, err
		}

		total++
	}

	if total == 0 {
		return 0, fmt.Errorf("%w: empty CPU list", ErrUnavailable)
	}

	return total, nil
}

func setFirst(properties map[string]string, key string, value string) {
	if _, exists := properties[key]; exists || value == "" {
		return
	}

	properties[key] = value
}

func readOptional(fs FileSource, path string) string {
	value, err := fs.ReadString(path)
	if err != nil {
		return ""
	}

	return value
}

func skipBlockDevice(name string) bool {
	return strings.HasPrefix(name, "loop") ||
		strings.HasPrefix(name, "ram") ||
		strings.HasPrefix(name, "zram")
}

func joinList(values []string) string {
	return strings.Join(values, ",")
}
