package builder

import (
	"fmt"
	"strings"

	"pkg.akt.dev/go/sdl"
)

// Confidential-compute persistent storage.
//
// On confidential runtimes `shared_fs = "none"`, so virtio-fs is disabled and a
// `volumeMode: Filesystem` PVC never reaches the guest — kata silently swaps in
// a tmpfs and the tenant's data is lost on restart. The durable path is a
// `volumeMode: Block` PVC, which hot-plugs into the guest as a raw device; the
// kata-agent then dm-crypts it with a per-lease key and mounts the decrypted
// filesystem at the tenant's mount path, entirely inside the TEE.
//
// The encryption key (DEK) is never placed in any Kubernetes object. The
// provider registers a per-lease DEK in the Key Broker Service (Trustee); the
// guest's attestation-agent retrieves it after attestation. The guest is told
// which KBS to use and which device→mount→key mapping to apply via the (already
// allow-listed) `kernel_params` Kata annotation:
//
//	agent.aa_kbc_params=cc_kbc::<kbs-url>
//	agent.secure_volumes=<devicePath>=<mountPath>=<keyResourceURI>[,...]
//
// The kata-agent (patched) parses `agent.secure_volumes`, fetches each
// key from KBS via CDH, and performs the dm-crypt open/format + mount.
const (
	// ccKernelParamsAnnotation is the Kata annotation that appends parameters to
	// the guest kernel command line. It is in the SNP/TDX config
	// `enable_annotations` list, so it can be set per-pod without touching the
	// shared kata configuration used by other confidential workloads.
	ccKernelParamsAnnotation = "io.katacontainers.config.hypervisor.kernel_params"

	// ccAAKBCParam points the guest attestation-agent at the KBS. "cc_kbc" is the
	// key-broker-client that speaks the KBS RCAR attestation protocol.
	ccAAKBCParam = "agent.aa_kbc_params"
	ccKBCName    = "cc_kbc"

	// ccSecureVolumesParam carries the per-volume device→mount→key mapping the
	// patched kata-agent consumes to set up confidential persistent storage.
	ccSecureVolumesParam = "agent.secure_volumes"
)

// ccSecureVolumeDevicePath is the in-container path the raw Block PVC device is
// surfaced at (via volumeDevices). It is intentionally namespaced so it does not
// collide with the tenant's own mounts; the tenant app uses the decrypted
// filesystem the kata-agent mounts at the SDL mount path, not this device.
func ccSecureVolumeDevicePath(volumeName string) string {
	return "/dev/akash_secure/" + volumeName
}

// ccSecureVolumeKeyURI is the KBS resource URI for a lease+volume's DEK. It is
// unique per lease (the lease namespace hash) and per volume, so tenants can
// never reference each other's keys. The provider registers the DEK here at
// deploy time; the guest retrieves it after attestation. Format is a KBS
// resource URI: kbs:///<repository>/<type>/<tag>.
func ccSecureVolumeKeyURI(leaseNS, volumeName string) string {
	return fmt.Sprintf("kbs:///%s/%s/dek", leaseNS, volumeName)
}

// ccPersistentVolume describes one confidential persistent volume of a service.
type ccPersistentVolume struct {
	// Name is the shared Kubernetes name of the PVC/volumeDevice ("<svc>-<vol>").
	Name string
	// VolumeName is the SDL storage name ("<vol>"), used to match storage params.
	VolumeName string
	// DevicePath is where the raw block device is attached in the container.
	DevicePath string
	// MountPath is where the tenant expects the decrypted filesystem (SDL mount).
	MountPath string
	// KeyURI is the KBS resource URI of this volume's DEK.
	KeyURI string
	// ReadOnly mirrors the SDL storage mount readOnly flag.
	ReadOnly bool
}

// CCVolumeKeyRef identifies a confidential persistent volume's DEK location in KBS.
type CCVolumeKeyRef struct {
	VolumeName string
	KeyURI     string
}

// CCPersistentVolumeKeyRefs returns the KBS DEK references for this workload's
// confidential persistent volumes (nil when none). The provider registers a DEK
// at each KeyURI before the guest attests and retrieves it.
func (b *Workload) CCPersistentVolumeKeyRefs() []CCVolumeKeyRef {
	vols := b.ccPersistentVolumes()
	if len(vols) == 0 {
		return nil
	}
	refs := make([]CCVolumeKeyRef, 0, len(vols))
	for _, v := range vols {
		refs = append(refs, CCVolumeKeyRef{VolumeName: v.VolumeName, KeyURI: v.KeyURI})
	}
	return refs
}

// isCC reports whether this service runs on a confidential runtime.
func (b *Workload) isCC() bool {
	params := b.sparams[b.serviceIdx]
	return params != nil && params.RuntimeClass.Is(WithCC())
}

// ccPersistentVolumes returns the confidential persistent volumes for this
// service, or nil when the service is not confidential or has no persistent
// storage. The returned mount paths come from the service's storage params.
func (b *Workload) ccPersistentVolumes() []ccPersistentVolume {
	if !b.isCC() {
		return nil
	}

	service := &b.group.Services[b.serviceIdx]

	// Map volume name -> (mount, readOnly) from the service storage params.
	mounts := map[string]struct {
		mount    string
		readOnly bool
	}{}
	if service.Params != nil {
		for _, p := range service.Params.Storage {
			mounts[p.Name] = struct {
				mount    string
				readOnly bool
			}{p.Mount, p.ReadOnly}
		}
	}

	var out []ccPersistentVolume
	var leaseNS string
	for _, storage := range service.Resources.Storage {
		if persistent, valid := storage.Attributes.Find(sdl.StorageAttributePersistent).AsBool(); !valid || !persistent {
			continue
		}

		// Resolve the lease namespace lazily: only when there is at least one
		// persistent volume, so callers with no durable storage never depend on
		// a deployment being set.
		if leaseNS == "" {
			leaseNS = b.NS()
		}

		m := mounts[storage.Name]
		out = append(out, ccPersistentVolume{
			Name:       fmt.Sprintf("%s-%s", service.Name, storage.Name),
			VolumeName: storage.Name,
			DevicePath: ccSecureVolumeDevicePath(storage.Name),
			MountPath:  m.mount,
			KeyURI:     ccSecureVolumeKeyURI(leaseNS, storage.Name),
			ReadOnly:   m.readOnly,
		})
	}

	return out
}

// ccSecureStorageKernelParams builds the value of the `kernel_params` annotation
// that wires the guest to KBS and describes each confidential persistent volume.
// It returns ("", false, nil) when the service has no confidential persistent
// storage. It returns an error when confidential persistent storage is requested
// but no KBS endpoint is configured (the feature cannot be honored) so callers
// can fail loud rather than silently lose data.
func (b *Workload) ccSecureStorageKernelParams() (string, bool, error) {
	vols := b.ccPersistentVolumes()
	if len(vols) == 0 {
		return "", false, nil
	}

	if b.settings.CCPersistenceKBSURL == "" {
		return "", false, fmt.Errorf("confidential persistent storage requested but no KBS endpoint configured (Settings.CCPersistenceKBSURL)")
	}

	specs := make([]string, 0, len(vols))
	for _, v := range vols {
		if v.MountPath == "" {
			return "", false, fmt.Errorf("confidential persistent volume %q has no mount path", v.Name)
		}
		specs = append(specs, strings.Join([]string{v.DevicePath, v.MountPath, v.KeyURI}, "="))
	}

	params := fmt.Sprintf("%s=%s::%s %s=%s",
		ccAAKBCParam, ccKBCName, b.settings.CCPersistenceKBSURL,
		ccSecureVolumesParam, strings.Join(specs, ","),
	)

	return params, true, nil
}
