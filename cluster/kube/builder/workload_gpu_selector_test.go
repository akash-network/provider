package builder

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// Test_nodeSelectorsFromResources_GPUInterface guards against the interface
// selector being keyed off the GPU memory size instead of the interface. The
// inventory operator publishes the node label as
// "<gpu-key>.interface.<interface>", so the selector must match that, otherwise
// a workload requesting a specific GPU interface can never be scheduled.
func Test_nodeSelectorsFromResources_GPUInterface(t *testing.T) {
	res := &crd.SchedulerResources{
		GPU: &crd.SchedulerResourceGPU{
			Vendor:     "nvidia",
			Model:      "a100",
			MemorySize: "80Gi",
			Interface:  "pcie",
		},
	}

	selectors := nodeSelectorsFromResources(res)

	key := fmt.Sprintf("%s.vendor.%s.model.%s", AkashServiceCapabilityGPU, "nvidia", "a100")
	wantInterfaceKey := fmt.Sprintf("%s.interface.%s", key, "pcie")
	badInterfaceKey := fmt.Sprintf("%s.interface.%s", key, "80Gi")

	keys := make(map[string]struct{}, len(selectors))
	for _, s := range selectors {
		keys[s.Key] = struct{}{}
	}

	require.Contains(t, keys, wantInterfaceKey, "interface selector must be keyed by the GPU interface")
	require.NotContains(t, keys, badInterfaceKey, "interface selector must not be keyed by the memory size")
}
