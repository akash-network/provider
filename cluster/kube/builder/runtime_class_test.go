package builder

import (
	"testing"

	"github.com/stretchr/testify/require"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

func TestRuntimeClassForTEEType(t *testing.T) {
	tests := []struct {
		name     string
		teeType  ctypes.TEEType
		platform ctypes.TEEPlatform
		want     RuntimeClass
		wantErr  bool
	}{
		{name: "SNP CPU", teeType: ctypes.TEETypeCPU, platform: ctypes.TEEPlatformSNP, want: RuntimeClassKataQemuSNP},
		{name: "SNP GPU", teeType: ctypes.TEETypeCPUGPU, platform: ctypes.TEEPlatformSNP, want: RuntimeClassKataQemuNvidiaGPUSNP},
		{name: "TDX CPU", teeType: ctypes.TEETypeCPU, platform: ctypes.TEEPlatformTDX, want: RuntimeClassKataQemuTDX},
		{name: "TDX GPU", teeType: ctypes.TEETypeCPUGPU, platform: ctypes.TEEPlatformTDX, want: RuntimeClassKataQemuNvidiaGPUTDX},
		{name: "missing platform", teeType: ctypes.TEETypeCPU, platform: ctypes.TEEPlatformNone, wantErr: true},
		{name: "unknown platform", teeType: ctypes.TEETypeCPU, platform: ctypes.TEEPlatform("future"), wantErr: true},
		{name: "missing TEE type", teeType: ctypes.TEETypeNone, platform: ctypes.TEEPlatformSNP, wantErr: true},
		{name: "unknown TEE type", teeType: ctypes.TEEType("future"), platform: ctypes.TEEPlatformSNP, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RuntimeClassForTEEType(tt.teeType, tt.platform)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

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
