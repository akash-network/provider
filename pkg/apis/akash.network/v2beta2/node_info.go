package v2beta2

type GPUCapabilities struct {
	Vendor string `json:"vendor" capabilities:"vendor"`
	Model  string `json:"string" capabilities:"model"`
}

type StorageCapabilities struct {
	Classes []string `json:"classes"`
}

type NodeInfoCapabilities struct {
	GPU     GPUCapabilities     `json:"gpu" capabilities:"gpu"`
	Storage StorageCapabilities `json:"storage" capabilities:"storage"`
}

func (c *StorageCapabilities) HasClass(class string) bool {
	for _, val := range c.Classes {
		if val == class {
			return true
		}
	}
	return false
}
