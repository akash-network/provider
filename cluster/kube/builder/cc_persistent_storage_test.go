package builder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	mani "pkg.akt.dev/go/manifest/v2beta3"
	attr "pkg.akt.dev/go/node/types/attributes/v1"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/sdl"
	"pkg.akt.dev/go/testutil"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

type ccStorageSpec struct {
	name       string
	mount      string
	persistent bool
	class      string
	readOnly   bool
}

func newCCStorageWorkload(t *testing.T, rc RuntimeClass, kbsURL string, specs ...ccStorageSpec) *Workload {
	t.Helper()

	var resStorage []rtypes.Storage
	var paramStorage []mani.StorageParams

	for _, s := range specs {
		attrs := attr.Attributes{}
		if s.persistent {
			attrs = append(attrs, attr.Attribute{Key: sdl.StorageAttributePersistent, Value: "true"})
		}
		if s.class != "" {
			attrs = append(attrs, attr.Attribute{Key: sdl.StorageAttributeClass, Value: s.class})
		}
		resStorage = append(resStorage, rtypes.Storage{
			Name:       s.name,
			Quantity:   rtypes.NewResourceValue(1 << 30),
			Attributes: attrs,
		})
		paramStorage = append(paramStorage, mani.StorageParams{Name: s.name, Mount: s.mount, ReadOnly: s.readOnly})
	}

	svc := mani.Service{
		Name:      "web",
		Resources: rtypes.Resources{Storage: resStorage},
		Params:    &mani.ServiceParams{Storage: paramStorage},
	}

	return &Workload{
		builder: builder{
			log:        testutil.Logger(t),
			settings:   Settings{CCPersistenceKBSURL: kbsURL},
			deployment: &ClusterDeployment{Lid: testutil.LeaseID(t)},
			group:      mani.Group{Services: mani.Services{svc}},
			sparams:    []*crd.SchedulerParams{{RuntimeClass: rc}},
		},
		serviceIdx: 0,
	}
}

const testKBSURL = "http://kbs.kbs.svc:8080"

// A confidential workload with a persistent volume must emit a Block-mode PVC,
// attach the device via volumeDevices (not volumeMounts), and wire the guest to
// KBS + the device→mount→key mapping via the kernel_params annotation.
func TestCCPersistentStorage_Wiring(t *testing.T) {
	wl := newCCStorageWorkload(t, RuntimeClassKataQemuSNP, testKBSURL,
		ccStorageSpec{name: "data", mount: "/data", persistent: true, class: "beta3"},
	)

	// PVC is Block mode.
	pvcs := wl.persistentVolumeClaims()
	require.Len(t, pvcs, 1)
	require.NotNil(t, pvcs[0].Spec.VolumeMode)
	require.Equal(t, corev1.PersistentVolumeBlock, *pvcs[0].Spec.VolumeMode)
	require.Equal(t, "web-data", pvcs[0].Name)

	// Container uses volumeDevices, not volumeMounts, for the persistent volume.
	c := wl.container()
	require.Len(t, c.VolumeDevices, 1, "persistent CC volume must be a raw block device")
	require.Equal(t, "web-data", c.VolumeDevices[0].Name)
	require.Equal(t, ccSecureVolumeDevicePath("data"), c.VolumeDevices[0].DevicePath)
	for _, vm := range c.VolumeMounts {
		require.NotEqual(t, "web-data", vm.Name, "persistent CC volume must not be a filesystem mount")
	}

	// kernel_params annotation wires KBS + the device→mount→key mapping.
	ann := wl.podAnnotations()
	kp, ok := ann[ccKernelParamsAnnotation]
	require.True(t, ok, "expected kernel_params annotation")
	require.Contains(t, kp, ccAAKBCParam+"="+ccKBCName+"::"+testKBSURL)
	require.Contains(t, kp, ccSecureVolumesParam+"=")
	// device=mount=keyURI triple present.
	require.Contains(t, kp, ccSecureVolumeDevicePath("data")+"=/data=kbs:///")
	// the DEK URI is scoped to the lease namespace (tenant isolation).
	require.Contains(t, kp, ccSecureVolumeKeyURI(wl.NS(), "data"))
}

// A non-confidential workload keeps the current behavior: Filesystem PVC,
// filesystem volumeMount, and no kernel_params annotation.
func TestCCPersistentStorage_NonCCUnchanged(t *testing.T) {
	wl := newCCStorageWorkload(t, RuntimeClass(""), testKBSURL,
		ccStorageSpec{name: "data", mount: "/data", persistent: true, class: "beta3"},
	)

	pvcs := wl.persistentVolumeClaims()
	require.Len(t, pvcs, 1)
	require.Equal(t, corev1.PersistentVolumeFilesystem, *pvcs[0].Spec.VolumeMode)

	c := wl.container()
	require.Empty(t, c.VolumeDevices, "non-CC volume must not be a block device")
	require.Len(t, c.VolumeMounts, 1)
	require.Equal(t, "/data", c.VolumeMounts[0].MountPath)

	_, ok := wl.podAnnotations()[ccKernelParamsAnnotation]
	require.False(t, ok, "non-CC workload must not get kernel_params annotation")
}

// Confidential + persistent but no KBS endpoint configured must fail loud
// (surface an error) rather than emit a half-wired annotation.
func TestCCPersistentStorage_NoKBSFailsLoud(t *testing.T) {
	wl := newCCStorageWorkload(t, RuntimeClassKataQemuSNP, "",
		ccStorageSpec{name: "data", mount: "/data", persistent: true, class: "beta3"},
	)

	_, ok, err := wl.ccSecureStorageKernelParams()
	require.Error(t, err, "missing KBS endpoint must be an error")
	require.False(t, ok)

	// podAnnotations swallows the error (logged) and must not emit the annotation.
	_, present := wl.podAnnotations()[ccKernelParamsAnnotation]
	require.False(t, present)
}

// Confidential workload with no persistent volume: no kernel_params annotation,
// no block devices.
func TestCCPersistentStorage_CCNoPersistent(t *testing.T) {
	wl := newCCStorageWorkload(t, RuntimeClassKataQemuSNP, testKBSURL,
		ccStorageSpec{name: "scratch", mount: "/scratch", persistent: false, class: "beta2"},
	)

	require.Empty(t, wl.ccPersistentVolumes())
	_, ok, err := wl.ccSecureStorageKernelParams()
	require.NoError(t, err)
	require.False(t, ok)

	_, present := wl.podAnnotations()[ccKernelParamsAnnotation]
	require.False(t, present)
}

// Multiple persistent volumes are each wired with a distinct device, mount, and
// per-volume key URI.
func TestCCPersistentStorage_MultipleVolumes(t *testing.T) {
	wl := newCCStorageWorkload(t, RuntimeClassKataQemuNvidiaGPUSNP, testKBSURL,
		ccStorageSpec{name: "data", mount: "/data", persistent: true, class: "beta3"},
		ccStorageSpec{name: "models", mount: "/models", persistent: true, class: "beta3"},
	)

	vols := wl.ccPersistentVolumes()
	require.Len(t, vols, 2)

	kp, _, err := wl.ccSecureStorageKernelParams()
	require.NoError(t, err)

	// one comma-separated triple per volume
	specs := strings.Split(strings.SplitN(kp, ccSecureVolumesParam+"=", 2)[1], ",")
	require.Len(t, specs, 2)
	require.Contains(t, kp, ccSecureVolumeDevicePath("data")+"=/data=")
	require.Contains(t, kp, ccSecureVolumeDevicePath("models")+"=/models=")
	require.NotEqual(t, vols[0].KeyURI, vols[1].KeyURI, "each volume must have a distinct key URI")
}
