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

type CPUArchitectureAttributes struct {
	Architectures []string
}

type CPUAttributes map[string]*CPUArchitectureAttributes

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
			return GPUAttributes{}, fmt.Errorf("invalid GPU attribute") // nolint: err113
		}

		switch tokens[0] {
		case "vendor":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[0]) // nolint: err113
		}

		switch tokens[2] {
		case "model":
		default:
			return GPUAttributes{}, fmt.Errorf("unexpected GPU attribute type (%s)", tokens[2]) // nolint: err113
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
						return GPUAttributes{}, fmt.Errorf("unsupported GPU interface (%s)", val) // nolint: err113
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
			return GPUAttributes{}, fmt.Errorf("unsupported GPU vendor (%s)", vendor) // nolint: err113
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

	if persistent && (class == "" || class == "ram") {
		return StorageAttributes{}, fmt.Errorf("persistent volume must specify storage class") // nolint: err113
	}

	res := StorageAttributes{
		Persistent: persistent,
		Class:      class,
	}

	return res, nil
}

func ParseCPUAttributes(attrs types.Attributes) (CPUAttributes, error) {
	cpuAttrs := make(CPUAttributes)

	for _, attr := range attrs {
		tokens := strings.Split(attr.Key, "/")
		if len(tokens) < 3 {
			return CPUAttributes{}, fmt.Errorf("invalid CPU attribute") // nolint: err113
		}

		switch tokens[0] {
		case "capabilities":
		default:
			return CPUAttributes{}, fmt.Errorf("unexpected CPU attribute type (%s)", tokens[0]) // nolint: err113
		}

		switch tokens[1] {
		case "cpu":
		default:
			return CPUAttributes{}, fmt.Errorf("unexpected CPU attribute type (%s)", tokens[1]) // nolint: err113
		}

		switch tokens[2] {
		case "arch":
		default:
			return CPUAttributes{}, fmt.Errorf("unexpected CPU attribute type (%s)", tokens[2]) // nolint: err113
		}

		if len(tokens) < 4 {
			return CPUAttributes{}, fmt.Errorf("invalid CPU architecture attribute") // nolint: err113
		}

		architecture := tokens[3]

		// Check if the attribute value is "true" to indicate support
		if attr.Value == "true" {
			if cpuAttrs["cpu"] == nil {
				cpuAttrs["cpu"] = &CPUArchitectureAttributes{
					Architectures: make([]string, 0),
				}
			}
			cpuAttrs["cpu"].Architectures = append(cpuAttrs["cpu"].Architectures, architecture)
		}
	}

	return cpuAttrs, nil
}

// ParseCPUAttributesFromSDL parses CPU architecture attributes from SDL format
// This supports the format: attributes: arch: [arm64, amd64]
func ParseCPUAttributesFromSDL(attrs map[string]interface{}) (CPUAttributes, error) {
	cpuAttrs := make(CPUAttributes)

	if archAttr, exists := attrs["arch"]; exists {
		switch v := archAttr.(type) {
		case []interface{}:
			architectures := make([]string, 0, len(v))
			for _, arch := range v {
				if archStr, ok := arch.(string); ok {
					architectures = append(architectures, archStr)
				} else {
					return CPUAttributes{}, fmt.Errorf("invalid architecture type in SDL") // nolint: err113
				}
			}
			cpuAttrs["cpu"] = &CPUArchitectureAttributes{
				Architectures: architectures,
			}
		case string:
			// Single architecture as string
			cpuAttrs["cpu"] = &CPUArchitectureAttributes{
				Architectures: []string{v},
			}
		default:
			return CPUAttributes{}, fmt.Errorf("invalid architecture format in SDL") // nolint: err113
		}
	}

	return cpuAttrs, nil
}

func (c *CPUArchitectureAttributes) HasArchitecture(arch string) bool {
	for _, supportedArch := range c.Architectures {
		if supportedArch == arch || supportedArch == "*" {
			return true
		}
	}
	return false
}

// ConvertSDLCPUAttributesToKeyValue converts SDL format CPU attributes to key-value format
// This function handles the conversion from: attributes: arch: [arm64, amd64]
// To: attributes: [{key: "capabilities/cpu/arch/arm64", value: "true"}, {key: "capabilities/cpu/arch/amd64", value: "true"}]
func ConvertSDLCPUAttributesToKeyValue(sdlAttrs map[string]interface{}) (types.Attributes, error) {
	var attrs types.Attributes

	if archAttr, exists := sdlAttrs["arch"]; exists {
		switch v := archAttr.(type) {
		case []interface{}:
			for _, arch := range v {
				if archStr, ok := arch.(string); ok {
					attrs = append(attrs, types.Attribute{
						Key:   fmt.Sprintf("capabilities/cpu/arch/%s", archStr),
						Value: "true",
					})
				} else {
					return nil, fmt.Errorf("invalid architecture type in SDL") // nolint: err113
				}
			}
		case string:
			// Single architecture as string
			attrs = append(attrs, types.Attribute{
				Key:   fmt.Sprintf("capabilities/cpu/arch/%s", v),
				Value: "true",
			})
		default:
			return nil, fmt.Errorf("invalid architecture format in SDL") // nolint: err113
		}
	}

	return attrs, nil
}
