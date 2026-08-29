package inventory

import (
	"testing"

	"github.com/stretchr/testify/require"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

func TestAdjustRejectsUnresolvedTEEPlatformBeforeInventoryMutation(t *testing.T) {
	tests := []struct {
		name     string
		platform ctypes.TEEPlatform
	}{
		{name: "missing", platform: ctypes.TEEPlatformNone},
		{name: "unknown", platform: ctypes.TEEPlatform("future")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := &inventory{}
			before := inv.Dup()
			err := inv.Adjust(nil,
				ctypes.WithTEEType(ctypes.TEETypeCPUGPU),
				ctypes.WithTEEPlatform(tt.platform),
			)
			require.ErrorContains(t, err, "unsupported TEE platform")
			require.Equal(t, before, inv.Dup())
		})
	}
}
