package hostprobe

import (
	"sort"
	"strings"
)

func DetectVirtualization(results []SourceResult) VirtualizationReport {
	signals := make([]VirtualizationSignal, 0)
	hypervisors := make(map[string]struct{})

	for _, result := range results {
		if result.Status != SourceStatusOK {
			continue
		}

		for key, value := range result.Properties {
			method, hypervisor, ok := virtualizationSignal(result, key, value)
			if !ok {
				continue
			}

			signals = append(signals, VirtualizationSignal{
				Source: result.Name,
				Method: method,
				Value:  value,
			})

			if hypervisor != "" {
				hypervisors[hypervisor] = struct{}{}
			}
		}
	}

	sort.Slice(signals, func(i, j int) bool {
		if signals[i].Method == signals[j].Method {
			return signals[i].Source < signals[j].Source
		}

		return signals[i].Method < signals[j].Method
	})

	methods := uniqueMethods(signals)
	names := make([]string, 0, len(hypervisors))
	for name := range hypervisors {
		names = append(names, name)
	}
	sort.Strings(names)

	return VirtualizationReport{
		Detected:   len(signals) > 0,
		Hypervisor: joinList(names),
		Methods:    methods,
		Evidence:   signals,
	}
}

func virtualizationSignal(result SourceResult, key string, value string) (string, string, bool) {
	lower := strings.ToLower(value)

	if result.HardwareClass == hardwareClassCPU && key == "flags" && hasCPUFlag(lower, "hypervisor") {
		return "cpuinfo_hypervisor_flag", "", true
	}

	if result.HardwareClass == hardwareClassPlatform {
		if hypervisor := knownVirtualPlatform(lower); hypervisor != "" {
			return "dmi_platform", hypervisor, true
		}
	}

	if key == "virtual_devices" && strings.TrimSpace(value) != "" {
		return "pci_virtual_device", knownVirtualPCI(value), true
	}

	return "", "", false
}

func hasCPUFlag(flags string, flag string) bool {
	for _, value := range strings.Fields(flags) {
		if value == flag {
			return true
		}
	}

	return false
}

func knownVirtualPlatform(value string) string {
	for marker, name := range map[string]string{
		"kvm":            "kvm",
		"qemu":           "qemu",
		"vmware":         "vmware",
		"virtualbox":     "virtualbox",
		"microsoft":      "hyper-v",
		"xen":            "xen",
		"parallels":      "parallels",
		"bhyve":          "bhyve",
		"openstack":      "openstack",
		"amazon ec2":     "nitro",
		"google compute": "gce",
	} {
		if strings.Contains(value, marker) {
			return name
		}
	}

	return ""
}

func isVirtualPCIDevice(vendor string, device string) bool {
	value := strings.ToLower(strings.TrimSpace(vendor + ":" + device))
	return strings.HasPrefix(value, "0x1af4:") ||
		strings.HasPrefix(value, "0x15ad:") ||
		strings.HasPrefix(value, "0x1414:") ||
		strings.HasPrefix(value, "0x5853:")
}

func knownVirtualPCI(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "0x1af4:"):
		return "virtio"
	case strings.Contains(lower, "0x15ad:"):
		return "vmware"
	case strings.Contains(lower, "0x1414:"):
		return "hyper-v"
	case strings.Contains(lower, "0x5853:"):
		return "xen"
	default:
		return ""
	}
}

func uniqueMethods(signals []VirtualizationSignal) []string {
	seen := make(map[string]struct{})
	methods := make([]string, 0, len(signals))
	for _, signal := range signals {
		if _, exists := seen[signal.Method]; exists {
			continue
		}

		seen[signal.Method] = struct{}{}
		methods = append(methods, signal.Method)
	}

	return methods
}
