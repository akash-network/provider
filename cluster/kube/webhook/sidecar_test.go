package webhook

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
)

func strPtr(s string) *string { return &s }

func TestShouldInject_CCPodWithGPU(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuNvidiaGPUSNP),
		},
	}
	require.True(t, ShouldInject(pod))
}

func TestShouldInject_CCPodCPUOnly(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
		},
	}
	require.True(t, ShouldInject(pod))
}

func TestShouldInject_NonCCPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr("nvidia"),
		},
	}
	require.False(t, ShouldInject(pod))
}

func TestShouldInject_NoRuntimeClass(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{},
	}
	require.False(t, ShouldInject(pod))
}

func TestShouldInject_AttestationDisabled(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{builder.AkashManagedLabelName: "true"},
			Annotations: map[string]string{builder.AkashAttestationDisabledAnnotation: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
		},
	}
	require.False(t, ShouldInject(pod))
}

func TestShouldInject_MissingLabel(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
		},
	}
	require.False(t, ShouldInject(pod))
}

func TestShouldInject_AlreadyInjected(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
			Containers: []corev1.Container{
				{Name: sidecarContainerName},
			},
		},
	}
	require.False(t, ShouldInject(pod))
}

func TestShouldInject_TDXCPUOnly(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuTDX),
		},
	}
	require.True(t, ShouldInject(pod))
}

func TestShouldInject_TDXWithGPU(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuNvidiaGPUTDX),
		},
	}
	require.True(t, ShouldInject(pod))
}

func TestBuildSidecarPatch_GPU(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuNvidiaGPUSNP),
			Containers: []corev1.Container{
				{Name: "workload", Image: "myimage"},
			},
		},
	}

	patchBytes, err := BuildSidecarPatch(pod, "ghcr.io/akash-network/attestation-sidecar:latest", nil)
	require.NoError(t, err)
	require.NotEmpty(t, patchBytes)

	var patches []jsonPatch
	err = json.Unmarshal(patchBytes, &patches)
	require.NoError(t, err)

	// GPU mode: container patch only, no volumes
	require.Len(t, patches, 1)

	// Verify NVIDIA Container Toolkit env vars are set on the sidecar
	containerPatch := patches[0]
	containerJSON, _ := json.Marshal(containerPatch.Value)
	var container corev1.Container
	require.NoError(t, json.Unmarshal(containerJSON, &container))

	envMap := make(map[string]string)
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}
	require.Equal(t, "all", envMap["NVIDIA_VISIBLE_DEVICES"])
	require.Equal(t, "utility", envMap["NVIDIA_DRIVER_CAPABILITIES"])
}

func TestBuildSidecarPatch_CPUOnly(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{builder.AkashManagedLabelName: "true"},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
			Containers: []corev1.Container{
				{Name: "workload", Image: "myimage"},
			},
		},
	}

	patchBytes, err := BuildSidecarPatch(pod, "ghcr.io/akash-network/attestation-sidecar:latest", nil)
	require.NoError(t, err)

	var patches []jsonPatch
	err = json.Unmarshal(patchBytes, &patches)
	require.NoError(t, err)

	// Production mode: container patch only, no volumes (guest kernel provides devices)
	require.Len(t, patches, 1)
}

func TestBuildSidecarPatch_EmptyImage(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			RuntimeClassName: strPtr(builder.RuntimeClassKataQemuSNP),
		},
	}

	_, err := BuildSidecarPatch(pod, "", nil)
	require.Error(t, err)
}
