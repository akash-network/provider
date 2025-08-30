package v2beta2

import (
	"math"
	"testing"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	atestutil "pkg.akt.dev/go/testutil"
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
		Resources: rtypes.Resources{
			ID: 1,
			CPU: &rtypes.CPU{
				Units: rtypes.NewResourceValue(math.MaxUint32),
			},
			Memory: &rtypes.Memory{
				Quantity: rtypes.NewResourceValue(math.MaxUint64),
			},
			GPU: &rtypes.GPU{
				Units: rtypes.NewResourceValue(math.MaxUint32),
			},
			Storage: rtypes.Volumes{
				rtypes.Storage{
					Quantity: rtypes.NewResourceValue(math.MaxUint64),
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
