package inventory

type RegistryGPUDevice struct {
	Name       string `json:"name"`
	Interface  string `json:"interface"`
	MemorySize string `json:"memory_size"`
}

type RegistryGPUDevices map[string]RegistryGPUDevice

type RegistryGPUVendor struct {
	Name    string             `json:"name"`
	Devices RegistryGPUDevices `json:"devices"`
}

type RegistryGPUVendors map[string]RegistryGPUVendor
