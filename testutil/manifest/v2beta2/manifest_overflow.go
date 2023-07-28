package v2beta2

import (
	"math"
	"testing"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	atestutil "github.com/akash-network/node/testutil"
)

// OverflowManifestGenerator generates a manifest maximum integer values
var OverflowManifestGenerator Generator = manifestGeneratorOverflow{}

type manifestGeneratorOverflow struct{}

func (mg manifestGeneratorOverflow) Manifest(t testing.TB) manifest.Manifest {
	t.Helper()
	return []manifest.Group{
		mg.Group(t),
	}
}

func (mg manifestGeneratorOverflow) Group(t testing.TB) manifest.Group {
	t.Helper()
	return manifest.Group{
		Name: atestutil.Name(t, "manifest-group"),
		Services: []manifest.Service{
			mg.Service(t),
		},
	}
}

func (mg manifestGeneratorOverflow) Service(t testing.TB) manifest.Service {
	t.Helper()
	return manifest.Service{
		Name:  "demo",
		Image: "quay.io/ovrclk/demo-app",
		Args:  []string{"run"},
		Env:   []string{"AKASH_TEST_SERVICE=true"},
		Resources: types.Resources{
			ID: 1,
			CPU: &types.CPU{
				Units: types.NewResourceValue(math.MaxUint32),
			},
			Memory: &types.Memory{
				Quantity: types.NewResourceValue(math.MaxUint64),
			},
			GPU: &types.GPU{
				Units: types.NewResourceValue(math.MaxUint32),
			},
			Storage: types.Volumes{
				types.Storage{
					Quantity: types.NewResourceValue(math.MaxUint64),
				},
			},
		},
		Count: math.MaxUint32,
		Expose: []manifest.ServiceExpose{
			mg.ServiceExpose(t),
		},
	}
}

func (mg manifestGeneratorOverflow) ServiceExpose(t testing.TB) manifest.ServiceExpose {
	t.Helper()
	return manifest.ServiceExpose{
		Port:         math.MaxUint16,
		ExternalPort: math.MaxUint16,
		Proto:        "TCP",
		Service:      "svc",
		Global:       true,
		Hosts: []string{
			atestutil.Hostname(t),
		},
	}
}
