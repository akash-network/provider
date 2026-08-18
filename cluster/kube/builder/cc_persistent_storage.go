package builder

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	mtypes "pkg.akt.dev/go/node/market/v1"
	"pkg.akt.dev/go/sdl"
)

const (
	ccSecureVolumeDevicePrefix = "/dev/akash_secure/"
	ccSecureVolumeMaxCount     = 16
	ccSealedKeyRefMaxBytes     = 64 * 1024
)

var ccSealedKeyRefPattern = regexp.MustCompile(`^sealed\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

type ccPersistentStorageResource struct {
	persistent   bool
	storageClass string
}

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

	persistentResources := make(map[string]ccPersistentStorageResource)
	for _, storage := range service.Resources.Storage {
		if _, duplicate := persistentResources[storage.Name]; duplicate {
			return nil, fmt.Errorf("duplicate storage resource %q", storage.Name)
		}
		persistent, _ := storage.Attributes.Find(sdl.StorageAttributePersistent).AsBool()
		storageClass, ok := storage.Attributes.Find(sdl.StorageAttributeClass).AsString()
		if !ok || storageClass == "" {
			storageClass = sdl.StorageClassDefault
		}
		persistentResources[storage.Name] = ccPersistentStorageResource{
			persistent:   persistent,
			storageClass: storageClass,
		}
	}

	paramsByName := make(map[string]struct{}, len(service.Params.Storage))
	mountPaths := make(map[string]string, len(service.Params.Storage))
	volumes := make([]ccPersistentVolume, 0, len(service.Params.Storage))
	for _, params := range service.Params.Storage {
		if _, duplicate := paramsByName[params.Name]; duplicate {
			return nil, fmt.Errorf("duplicate storage parameters for %q", params.Name)
		}
		paramsByName[params.Name] = struct{}{}

		resource, known := persistentResources[params.Name]
		if !known {
			return nil, fmt.Errorf("storage parameters reference unknown resource %q", params.Name)
		}
		if params.KeyRef != "" && !resource.persistent {
			return nil, fmt.Errorf("storage %q keyRef requires a persistent resource", params.Name)
		}
		if !resource.persistent {
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
		if _, allowed := b.settings.CCPersistentStorageClasses[resource.storageClass]; !allowed {
			return nil, fmt.Errorf(
				"confidential persistent storage %q class %q is not allowlisted by the provider",
				params.Name,
				resource.storageClass,
			)
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

		volumeID, err := ccPersistentVolumeID(
			b.deployment.LeaseID(),
			service.Name,
			params.Name,
			params.KeyRef,
		)
		if err != nil {
			return nil, fmt.Errorf("derive confidential persistent storage %q identity: %w", params.Name, err)
		}

		volumes = append(volumes, ccPersistentVolume{
			Name:       fmt.Sprintf("%s-%s", service.Name, params.Name),
			DevicePath: ccSecureVolumeDevicePrefix + params.Name,
			MountPath:  params.Mount,
			KeyRef:     params.KeyRef,
			ReadOnly:   false,
			VolumeID:   volumeID,
		})
	}

	if len(volumes) != 0 && service.Count != 1 {
		return nil, fmt.Errorf("confidential persistent storage requires exactly one replica")
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

func ccPersistentVolumeID(
	leaseID mtypes.LeaseID,
	serviceName string,
	volumeName string,
	keyRef string,
) (string, error) {
	payload, err := ccSealedKeyRefPayload(keyRef)
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte("akash-confidential-volume-id-v2\x00"))
	for _, field := range [][]byte{
		[]byte(leaseID.Owner),
		[]byte(strconv.FormatUint(leaseID.DSeq, 10)),
		[]byte(strconv.FormatUint(uint64(leaseID.GSeq), 10)),
		[]byte(strconv.FormatUint(uint64(leaseID.OSeq), 10)),
		[]byte(leaseID.Provider),
		[]byte(strconv.FormatUint(uint64(leaseID.BSeq), 10)),
		[]byte(serviceName),
		[]byte(volumeName),
		payload,
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(field)
	}

	return "akash:v2:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func ccSealedKeyRefPayload(keyRef string) ([]byte, error) {
	if len(keyRef) > ccSealedKeyRefMaxBytes || !ccSealedKeyRefPattern.MatchString(keyRef) {
		return nil, fmt.Errorf("invalid sealed keyRef")
	}

	parts := strings.Split(keyRef, ".")
	if len(parts) != 4 || parts[0] != "sealed" {
		return nil, fmt.Errorf("invalid sealed keyRef")
	}

	decoded := make([][]byte, 3)
	for index, encoded := range parts[1:] {
		value, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(value) == 0 || base64.RawURLEncoding.EncodeToString(value) != encoded {
			return nil, fmt.Errorf("invalid sealed keyRef")
		}
		decoded[index] = value
	}

	return decoded[1], nil
}

func (b *Workload) confidentialPersistentVolume(name string) (ccPersistentVolume, bool) {
	for _, volume := range b.secureVolumes {
		if volume.Name == name {
			return volume, true
		}
	}
	return ccPersistentVolume{}, false
}
