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

	// Should have container patch + volumes array patch
	require.Len(t, patches, 2) // container + volumes
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

	// Should have container patch + volumes array patch (TSM + sev-guest in one array)
	require.Len(t, patches, 2)
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
