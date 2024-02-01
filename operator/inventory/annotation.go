package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	inventoryV1 "github.com/akash-network/akash-api/go/inventory/v1"
	"github.com/blang/semver/v4"
	"gopkg.in/yaml.v3"
)

const (
	annotationVersionField = "version"

	AnnotationKeyCapabilitiesSHA256 = "akash.network/capabilities.sha256"
	AnnotationKeyCapabilities       = "akash.network/capabilities"
	AnnotationNodeSelfCapabilities  = "akash.network/node.self.capabilities"
)

var (
	errCapabilitiesInvalid            = errors.New("capabilities: invalid")
	errCapabilitiesInvalidContent     = fmt.Errorf("%w: content", errCapabilitiesInvalid)
	errCapabilitiesInvalidNoVersion   = fmt.Errorf("%w: no version found", errCapabilitiesInvalid)
	errCapabilitiesInvalidVersion     = fmt.Errorf("%w: version", errCapabilitiesInvalid)
	errCapabilitiesUnsupportedVersion = fmt.Errorf("%w: unsupported version", errCapabilitiesInvalid)
)

type CapabilitiesV1GPU struct {
	Vendor     string `json:"vendor"`
	VendorID   string `json:"vendor_id"`
	Model      string `json:"model"`
	ModelID    string `json:"model_id"`
	MemorySize string `json:"memory_size"`
	Interface  string `json:"interface"`
}

// type CapabilitiesV1GPUs []CapabilitiesV1GPU

type CapabilitiesV1 struct {
	StorageClasses []string             `json:"storage_classes"`
	GPUs           inventoryV1.GPUInfoS `json:"gpus"`
}

type Capabilities interface{}

type AnnotationCapabilities struct {
	Version           semver.Version `json:"version" yaml:"version"`
	LastAppliedSHA256 string         `json:"last_applied_sha256" yaml:"last_applied_sha256"`
	Capabilities      `yaml:",inline"`
}

var (
	_ json.Marshaler   = (*AnnotationCapabilities)(nil)
	_ json.Unmarshaler = (*AnnotationCapabilities)(nil)
)

func remove[T any](slice []T, s int) []T {
	return append(slice[:s], slice[s+1:]...)
}

func NewAnnotationCapabilities(sc []string) *AnnotationCapabilities {
	caps := &CapabilitiesV1{
		StorageClasses: make([]string, len(sc)),
	}

	copy(caps.StorageClasses, sc)

	res := &AnnotationCapabilities{
		Version:      semver.Version{Major: 1},
		Capabilities: caps,
	}

	return res
}

func NewAnnotationNodeSelfCapabilities(sc []string) *AnnotationCapabilities {
	caps := &CapabilitiesV1{
		StorageClasses: make([]string, len(sc)),
	}

	copy(caps.StorageClasses, sc)

	res := &AnnotationCapabilities{
		Version:      semver.Version{Major: 1},
		Capabilities: caps,
	}

	return res
}

func (s *CapabilitiesV1) RemoveClass(name string) bool {
	for i, c := range s.StorageClasses {
		if c == name {
			s.StorageClasses = remove(s.StorageClasses, i)
			sort.Strings(s.StorageClasses)
			return true
		}
	}

	return false
}

func parseNodeCapabilities(annotations map[string]string) (*AnnotationCapabilities, []byte, error) {
	res := &AnnotationCapabilities{}

	val, exists := annotations[AnnotationKeyCapabilities]
	if !exists {
		return res, []byte{}, nil
	}

	var err error
	if strings.HasPrefix(val, "{") {
		err = json.Unmarshal([]byte(val), res)
	} else {
		err = yaml.Unmarshal([]byte(val), res)
	}

	if err != nil {
		return nil, []byte{}, err
	}

	val, exists = annotations[AnnotationKeyCapabilitiesSHA256]
	if !exists {
		return res, []byte{}, nil
	}

	checksum, err := hex.DecodeString(val)
	if err != nil && len(checksum) != sha256.Size {
		return res, []byte{}, nil
	}

	return res, checksum, nil
}

func (s *AnnotationCapabilities) UnmarshalYAML(node *yaml.Node) error {
	var result AnnotationCapabilities

	foundVersion := false
	for idx := range node.Content {
		if node.Content[idx].Value == annotationVersionField {
			var err error
			if result.Version, err = semver.ParseTolerant(node.Content[idx+1].Value); err != nil {
				return fmt.Errorf("%w: %w", errCapabilitiesInvalidVersion, err)
			}
			foundVersion = true
			break
		}
	}

	if !foundVersion {
		return errCapabilitiesInvalidNoVersion
	}

	// nolint: gocritic
	switch result.Version.String() {
	case "1.0.0":
		var decoded CapabilitiesV1
		if err := node.Decode(&decoded); err != nil {
			return fmt.Errorf("%w: %w", errCapabilitiesInvalidContent, err)
		}

		sort.Strings(decoded.StorageClasses)

		result.Capabilities = &decoded
	default:
		return fmt.Errorf("%w: %q", errCapabilitiesUnsupportedVersion, result.Version)
	}

	*s = result

	return nil
}

func (s *AnnotationCapabilities) UnmarshalJSON(data []byte) error {
	core := make(map[string]interface{})

	err := json.Unmarshal(data, &core)
	if err != nil {
		return fmt.Errorf("%w: %w", errCapabilitiesInvalidContent, err)
	}

	result := AnnotationCapabilities{}

	if val, valid := core[annotationVersionField].(string); valid {
		if result.Version, err = semver.ParseTolerant(val); err != nil {
			return fmt.Errorf("%w: %w", errCapabilitiesInvalidVersion, err)
		}
	} else {
		return errCapabilitiesInvalidNoVersion
	}

	// nolint: gocritic
	switch result.Version.String() {
	case "1.0.0":
		var decoded CapabilitiesV1
		if err := json.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("%w: %w", errCapabilitiesInvalidContent, err)
		}

		sort.Strings(decoded.StorageClasses)

		result.Capabilities = &decoded
	default:
		return fmt.Errorf("%w: %q", errCapabilitiesUnsupportedVersion, result.Version)
	}

	*s = result

	return nil
}

// MarshalJSON bc at the time of writing Go 1.21 json/encoding does not support inline tag
// this function circumvents the issue by using temporary anonymous struct
func (s *AnnotationCapabilities) MarshalJSON() ([]byte, error) {
	var obj interface{}

	// remove no lint when next version added
	// nolint: gocritic
	switch caps := s.Capabilities.(type) {
	case *CapabilitiesV1:
		data, err := json.Marshal(caps)
		if err != nil {
			return nil, err
		}

		enc := sha256.New()
		if _, err = enc.Write(data); err != nil {
			return nil, err
		}

		obj = struct {
			Version           semver.Version `json:"version"`
			LastAppliedSHA256 string         `json:"last_applied_sha256"`
			CapabilitiesV1
		}{
			Version:           s.Version,
			LastAppliedSHA256: hex.EncodeToString(enc.Sum(nil)),
			CapabilitiesV1:    *caps,
		}
	}

	return json.Marshal(obj)
}
