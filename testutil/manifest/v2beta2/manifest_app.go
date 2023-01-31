package v2beta2

import (
	"testing"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	atestutil "github.com/akash-network/node/testutil"
	"github.com/akash-network/akash-api/go/node/types/unit"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

// AppManifestGenerator represents a real-world, deployable configuration.
var AppManifestGenerator Generator = manifestGeneratorApp{}

type manifestGeneratorApp struct{}

func (mg manifestGeneratorApp) Manifest(t testing.TB) manifest.Manifest {
	t.Helper()
	return []manifest.Group{
		mg.Group(t),
	}
}

func (mg manifestGeneratorApp) Group(t testing.TB) manifest.Group {
	t.Helper()
	return manifest.Group{
		Name: atestutil.Name(t, "manifest-group"),
		Services: []manifest.Service{
			mg.Service(t),
		},
	}
}

func (mg manifestGeneratorApp) Service(t testing.TB) manifest.Service {
	t.Helper()
	return manifest.Service{
		Name:  "demo",
		Image: "ropes/akash-app:v1",
		Resources: types.ResourceUnits{
			CPU: &types.CPU{
				Units: types.NewResourceValue(100),
			},
			Memory: &types.Memory{
				Quantity: types.NewResourceValue(128 * unit.Mi),
			},
			GPU: types.GPUs{
				types.GPU{
					Units: types.NewResourceValue(1000),
				},
			},
			Storage: types.Volumes{
				types.Storage{
					Quantity: types.NewResourceValue(256 * unit.Mi),
				},
			},
		},
		Count: 1,
		Expose: []manifest.ServiceExpose{
			mg.ServiceExpose(t),
		},
	}
}

func (mg manifestGeneratorApp) ServiceExpose(t testing.TB) manifest.ServiceExpose {
	return manifest.ServiceExpose{
		Port:    80,
		Service: "demo",
		Global:  true,
		Proto:   "TCP",
		Hosts: []string{
			atestutil.Hostname(t),
		},
	}
}
