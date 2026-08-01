package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"pkg.akt.dev/go/sdl"
)

const (
	ccSecureVolumeDevicePrefix = "/dev/akash_secure/"
	ccSecureVolumeMaxCount     = 16
	ccSealedKeyRefMaxBytes     = 64 * 1024
)

var ccSealedKeyRefPattern = regexp.MustCompile(`^sealed\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

type ccPersistentVolume struct {
	Name       string
	DevicePath string
	MountPath  string
	KeyRef     string
	ReadOnly   bool
	VolumeID   string
}

func (b *Workload) confidentialPersistentVolumes() ([]ccPersistentVolume, error) {
	service := &b.group.Services[b.serviceIdx]
	if service.Params == nil {
		return nil, nil
	}

	persistentResources := make(map[string]bool)
	for _, storage := range service.Resources.Storage {
		if _, duplicate := persistentResources[storage.Name]; duplicate {
			return nil, fmt.Errorf("duplicate storage resource %q", storage.Name)
		}
		persistent, _ := storage.Attributes.Find(sdl.StorageAttributePersistent).AsBool()
		persistentResources[storage.Name] = persistent
	}

	paramsByName := make(map[string]struct{}, len(service.Params.Storage))
	mountPaths := make(map[string]string, len(service.Params.Storage))
	volumes := make([]ccPersistentVolume, 0, len(service.Params.Storage))
	for _, params := range service.Params.Storage {
		if _, duplicate := paramsByName[params.Name]; duplicate {
			return nil, fmt.Errorf("duplicate storage parameters for %q", params.Name)
		}
		paramsByName[params.Name] = struct{}{}

		persistent, known := persistentResources[params.Name]
		if !known {
			return nil, fmt.Errorf("storage parameters reference unknown resource %q", params.Name)
		}
		if params.KeyRef != "" && !persistent {
			return nil, fmt.Errorf("storage %q keyRef requires a persistent resource", params.Name)
		}
		if !persistent {
			continue
		}

		isConfidential := b.sparams[b.serviceIdx] != nil && b.sparams[b.serviceIdx].RuntimeClass.Is(WithCC())
		if !isConfidential {
			if params.KeyRef != "" {
				return nil, fmt.Errorf("storage %q keyRef requires a confidential runtime", params.Name)
			}
			continue
		}
		if params.KeyRef == "" {
			return nil, fmt.Errorf("confidential persistent storage %q requires a tenant-signed keyRef", params.Name)
		}
		if params.ReadOnly {
			return nil, fmt.Errorf("confidential persistent storage %q does not support readOnly", params.Name)
		}
		if errs := validation.IsDNS1123Label(params.Name); len(errs) != 0 {
			return nil, fmt.Errorf("confidential persistent storage name %q is invalid: %s", params.Name, strings.Join(errs, "; "))
		}
		if !isNormalizedAbsolutePath(params.Mount) {
			return nil, fmt.Errorf("confidential persistent storage %q has an unsafe mount path", params.Name)
		}
		if existing, duplicate := mountPaths[params.Mount]; duplicate {
			return nil, fmt.Errorf(
				"confidential persistent storage %q duplicates mount path %q from %q",
				params.Name,
				params.Mount,
				existing,
			)
		}
		mountPaths[params.Mount] = params.Name
		if len(params.KeyRef) > ccSealedKeyRefMaxBytes || !ccSealedKeyRefPattern.MatchString(params.KeyRef) {
			return nil, fmt.Errorf("confidential persistent storage %q has an invalid sealed keyRef", params.Name)
		}

		volumes = append(volumes, ccPersistentVolume{
			Name:       fmt.Sprintf("%s-%s", service.Name, params.Name),
			DevicePath: ccSecureVolumeDevicePrefix + params.Name,
			MountPath:  params.Mount,
			KeyRef:     params.KeyRef,
			ReadOnly:   false,
			VolumeID:   ccPersistentVolumeID(service.Name, params.Name, params.KeyRef),
		})
	}

	if len(volumes) > ccSecureVolumeMaxCount {
		return nil, fmt.Errorf("confidential workload exceeds the %d persistent-volume limit", ccSecureVolumeMaxCount)
	}
	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].DevicePath < volumes[j].DevicePath
	})

	return volumes, nil
}

func isNormalizedAbsolutePath(value string) bool {
	return filepath.IsAbs(value) && value != string(filepath.Separator) && filepath.Clean(value) == value
}

func ccPersistentVolumeID(serviceName, volumeName, keyRef string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("akash-volume-v1\x00"))
	_, _ = hash.Write([]byte(serviceName))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(volumeName))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(keyRef))
	return "akash:v1:" + hex.EncodeToString(hash.Sum(nil))
}

func (b *Workload) confidentialPersistentVolume(name string) (ccPersistentVolume, bool) {
	for _, volume := range b.secureVolumes {
		if volume.Name == name {
			return volume, true
		}
	}
	return ccPersistentVolume{}, false
}
