package webhook

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/akash-network/provider/cluster/kube/builder"
)

const (
	sidecarContainerName = "akash-attestation-sidecar"
	sidecarPort          = int32(8790)
	tsmVolumeName        = "tsm-reports"
	sevGuestVolumeName   = "sev-guest"
)

// ShouldInject returns true if the pod should have the attestation sidecar injected.
// Trigger conditions:
//   - Pod has the akash.network managed label
//   - Pod has a Kata CC RuntimeClassName
//   - Tenant has not opted out via attestation-disabled annotation
//   - Sidecar is not already present (idempotency)
func ShouldInject(pod *corev1.Pod) bool {
	if pod.Labels[builder.AkashManagedLabelName] != builder.ValTrue {
		return false
	}

	if pod.Spec.RuntimeClassName == nil {
		return false
	}

	if !builder.IsConfidentialComputeRuntimeClass(*pod.Spec.RuntimeClassName) {
		return false
	}

	// Tenant opted out of attestation sidecar injection
	if pod.Annotations[builder.AkashAttestationDisabledAnnotation] == "true" {
		return false
	}

	for _, c := range pod.Spec.Containers {
		if c.Name == sidecarContainerName {
			return false // already injected
		}
	}

	return true
}

type jsonPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// BuildSidecarPatch generates a JSON Patch (RFC 6902) that injects the
// attestation sidecar container, required volumes, and volume mounts into
// the pod spec.
//
// The sidecar runs INSIDE the Kata VM (same pod, same VM as the tenant workload).
// It requires CAP_SYS_ADMIN for configfs mount operations within the guest kernel.
func BuildSidecarPatch(pod *corev1.Pod, sidecarImage string, extraEnv []corev1.EnvVar) ([]byte, error) {
	if sidecarImage == "" {
		return nil, fmt.Errorf("attestation sidecar image not configured")
	}

	runtimeClass := ""
	if pod.Spec.RuntimeClassName != nil {
		runtimeClass = *pod.Spec.RuntimeClassName
	}

	isGPU := builder.IsGPURuntimeClass(runtimeClass)

	mockMode := isMockMode(extraEnv)
	container := buildSidecarContainer(sidecarImage, isGPU, extraEnv)
	volumes := buildSidecarVolumes(isGPU, mockMode)

	var patches []jsonPatch

	// Add sidecar container
	if len(pod.Spec.Containers) == 0 {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/spec/containers",
			Value: []corev1.Container{container},
		})
	} else {
		patches = append(patches, jsonPatch{
			Op:    "add",
			Path:  "/spec/containers/-",
			Value: container,
		})
	}

	// Add volumes (only if there are any to add).
	if len(volumes) > 0 {
		if len(pod.Spec.Volumes) == 0 {
			patches = append(patches, jsonPatch{
				Op:    "add",
				Path:  "/spec/volumes",
				Value: volumes,
			})
		} else {
			for _, vol := range volumes {
				patches = append(patches, jsonPatch{
					Op:    "add",
					Path:  "/spec/volumes/-",
					Value: vol,
				})
			}
		}
	}

	return json.Marshal(patches)
}

func buildSidecarContainer(image string, isGPU bool, extraEnv []corev1.EnvVar) corev1.Container {
	c := corev1.Container{
		Name:            sidecarContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullAlways,
		Command:         []string{"/attestation-sidecar"},
		Ports: []corev1.ContainerPort{{
			Name:          "attestation",
			ContainerPort: sidecarPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		SecurityContext: &corev1.SecurityContext{
			Privileged: func() *bool { t := true; return &t }(),
			RunAsUser:  func() *int64 { uid := int64(0); return &uid }(),
		},
		Resources: sidecarResources(isGPU),
		Env: append([]corev1.EnvVar{
			{Name: "ATTESTATION_LISTEN_ADDR", Value: fmt.Sprintf(":%d", sidecarPort)},
		}, extraEnv...),
	}

	// Volume mounts: in mock mode use writable paths (distroless /sys is read-only).
	// In production, Kata VMs have a writable guest /sys/kernel/config.
	mockMode := isMockMode(extraEnv)
	if mockMode {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      tsmVolumeName,
			MountPath: "/var/run/mock-tee/tsm-report",
		})
		if !isGPU {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      sevGuestVolumeName,
				MountPath: "/var/run/mock-tee/sev-guest",
			})
		}
	}
	// Production (non-mock) kata VMs: the sidecar accesses /dev/sev-guest
	// and /sys/kernel/config/tsm/report directly from the guest kernel
	// filesystem — no volume mounts needed.

	// GPU CC pods: tell the NVIDIA Container Toolkit to inject driver
	// binaries (nvidia-smi) and libraries (libnvidia-ml.so) into this
	// container. The "utility" capability provides nvidia-smi + NVML
	// without consuming a GPU device allocation.
	if isGPU {
		c.Env = append(c.Env,
			corev1.EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
			corev1.EnvVar{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "utility"},
		)
	}

	return c
}

func sidecarResources(isGPU bool) corev1.ResourceRequirements {
	memRequest := builder.SidecarMemoryRequestBytes
	memLimit := builder.SidecarMemoryLimitBytes
	if isGPU {
		memRequest = builder.SidecarGPUMemoryRequestBytes
		memLimit = builder.SidecarGPUMemoryLimitBytes
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(builder.SidecarCPURequestMillicores, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memRequest, resource.DecimalSI),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(builder.SidecarCPULimitMillicores, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(memLimit, resource.DecimalSI),
		},
	}
}

func isMockMode(extraEnv []corev1.EnvVar) bool {
	for _, env := range extraEnv {
		if env.Name == "ATTESTATION_MOCK" && env.Value == "true" {
			return true
		}
	}
	return false
}

func buildSidecarVolumes(isGPU bool, mockMode bool) []corev1.Volume {
	// Mock mode: use emptyDir so pods can start on non-TEE hosts (Kind).
	// The mock TEE provider generates synthetic reports in-memory and
	// doesn't access these mount paths.
	if mockMode {
		volumes := []corev1.Volume{
			{Name: tsmVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		}
		if !isGPU {
			volumes = append(volumes, corev1.Volume{
				Name: sevGuestVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			})
		}
		return volumes
	}

	// Production: the sidecar accesses /dev/sev-guest and
	// /sys/kernel/config/tsm/report directly from the kata guest kernel
	// filesystem. HostPath volumes cannot be used because kubelet validates
	// paths on the host node, where TEE devices do not exist.
	// GPU CC: nvidia-smi and libs are bundled in the sidecar image.
	return nil
}
