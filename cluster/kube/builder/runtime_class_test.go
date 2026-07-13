package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeClass_Is_NoOptions(t *testing.T) {
	for _, rc := range []RuntimeClass{
		RuntimeClassKataQemuSNP,
		RuntimeClassKataQemuNvidiaGPUSNP,
		RuntimeClassKataQemuTDX,
		RuntimeClassKataQemuNvidiaGPUTDX,
	} {
		require.True(t, rc.Is(), "expected %q to be a known runtime class", rc)
	}
}

func TestRuntimeClass_Is_Unknown(t *testing.T) {
	for _, rc := range []RuntimeClass{"", "none", "nvidia", "runc", "kata-qemu"} {
		require.False(t, rc.Is(), "expected %q to NOT be a known runtime class", rc)
	}
}

func TestRuntimeClass_Is_WithCC(t *testing.T) {
	for _, rc := range []RuntimeClass{
		RuntimeClassKataQemuSNP,
		RuntimeClassKataQemuNvidiaGPUSNP,
		RuntimeClassKataQemuTDX,
		RuntimeClassKataQemuNvidiaGPUTDX,
	} {
		require.True(t, rc.Is(WithCC()), "%q should match WithCC", rc)
	}

	require.False(t, RuntimeClass("nvidia").Is(WithCC()))
	require.False(t, RuntimeClass("").Is(WithCC()))
}

func TestRuntimeClass_Is_WithGPU(t *testing.T) {
	require.True(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithGPU()))
	require.True(t, RuntimeClassKataQemuNvidiaGPUTDX.Is(WithGPU()))

	require.False(t, RuntimeClassKataQemuSNP.Is(WithGPU()))
	require.False(t, RuntimeClassKataQemuTDX.Is(WithGPU()))
	require.False(t, RuntimeClass("nvidia").Is(WithGPU()))
}

func TestRuntimeClass_Is_WithSNP(t *testing.T) {
	require.True(t, RuntimeClassKataQemuSNP.Is(WithSNP()))
	require.True(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithSNP()))

	require.False(t, RuntimeClassKataQemuTDX.Is(WithSNP()))
	require.False(t, RuntimeClassKataQemuNvidiaGPUTDX.Is(WithSNP()))
}

func TestRuntimeClass_Is_WithTDX(t *testing.T) {
	require.True(t, RuntimeClassKataQemuTDX.Is(WithTDX()))
	require.True(t, RuntimeClassKataQemuNvidiaGPUTDX.Is(WithTDX()))

	require.False(t, RuntimeClassKataQemuSNP.Is(WithTDX()))
	require.False(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithTDX()))
}

func TestRuntimeClass_Is_CombinedOptions(t *testing.T) {
	require.True(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithSNP(), WithGPU()))
	require.True(t, RuntimeClassKataQemuNvidiaGPUTDX.Is(WithTDX(), WithGPU()))
	require.True(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithCC(), WithGPU(), WithSNP()))

	require.False(t, RuntimeClassKataQemuSNP.Is(WithSNP(), WithGPU()),
		"SNP CPU-only should not match WithSNP+WithGPU")
	require.False(t, RuntimeClassKataQemuTDX.Is(WithTDX(), WithGPU()),
		"TDX CPU-only should not match WithTDX+WithGPU")
	require.False(t, RuntimeClassKataQemuNvidiaGPUSNP.Is(WithTDX(), WithGPU()),
		"SNP GPU should not match WithTDX+WithGPU")
}
