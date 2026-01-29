package v2beta2

import "slices"

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
	return slices.Contains(c.Classes, class)
}
