package inventory

import (
	"fmt"
	"strings"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/akash-api/go/util/units"

	"github.com/akash-network/node/sdl"
)

type GPUModelAttributes struct {
	RAM       string
	Interface string
}

type GPUModels map[string]*GPUModelAttributes

type GPUAttributes map[string]GPUModels

type StorageAttributes struct {
	Persistent bool   `json:"persistent"`
	Class      string `json:"class,omitempty"`
}

func (m GPUModels) ExistsOrWildcard(model string) (*GPUModelAttributes, bool) {
	attr, exists := m[model]
	if exists {
		return attr, true
	}

	attr, exists = m["*"]

	return attr, exists
}

func ParseGPUAttributes(attrs types.Attributes) (GPUAttributes, error) {
	nvidia := make(GPUModels)
	amd := make(GPUModels)

	for _, attr := range attrs {
		tokens := strings.Split(attr.Key, "/")
		if len(tokens) < 4 || len(tokens)%2 != 0 {
			return GPUAttributes{}, fmt.Errorf("invalid GPU attribute") // nolint: goerr113
		}

		switch tokens[0] {
		case "vendor":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[0]) // nolint: goerr113
		}

		switch tokens[2] {
		case "model":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[2]) // nolint: goerr113
		}

		vendor := tokens[1]
		model := tokens[3]

		var mattrs *GPUModelAttributes

		if len(tokens) > 4 {
			mattrs = &GPUModelAttributes{}

			tokens = tokens[4:]

			for i := 0; i < len(tokens); i += 2 {
				key := tokens[i]
				val := tokens[i+1]

				switch key {
				case "ram":
					q, err := units.MemoryQuantityFromString(val)
					if err != nil {
						return GPUAttributes{}, err
					}

					mattrs.RAM = q.StringWithSuffix("Gi")
				case "interface":
					switch val {
					case "pcie":
					case "sxm":
					default:
						return GPUAttributes{}, fmt.Errorf("unsupported GPU interface (%s)", val) // nolint: goerr113
					}

					mattrs.Interface = val
				default:
				}
			}
		}

		switch vendor {
		case "nvidia":
			nvidia[model] = mattrs
		case "amd":
			amd[model] = mattrs
		default:
			return GPUAttributes{}, fmt.Errorf("unsupported GPU vendor (%s)", vendor) // nolint: goerr113
		}

	}

	res := make(GPUAttributes)
	if len(nvidia) > 0 {
		res["nvidia"] = nvidia
	}

	if len(amd) > 0 {
		res["amd"] = amd
	}

	return res, nil
}

func ParseStorageAttributes(attrs types.Attributes) (StorageAttributes, error) {
	attr := attrs.Find(sdl.StorageAttributePersistent)
	persistent, _ := attr.AsBool()
	attr = attrs.Find(sdl.StorageAttributeClass)
	class, _ := attr.AsString()

	if persistent && class == "" {
		return StorageAttributes{}, fmt.Errorf("persistent volume must specify storage class") // nolint: goerr113
	}

	res := StorageAttributes{
		Persistent: persistent,
		Class:      class,
	}

	return res, nil
}
