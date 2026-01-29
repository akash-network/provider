package v2beta2

import (
	"testing"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/node/types/unit"
	"pkg.akt.dev/go/testutil"
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
		Name: testutil.Name(t, "manifest-group"),
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
		Resources: rtypes.Resources{
			ID: 1,
			CPU: &rtypes.CPU{
				Units: rtypes.NewResourceValue(100),
			},
			Memory: &rtypes.Memory{
				Quantity: rtypes.NewResourceValue(128 * unit.Mi),
			},
			GPU: &rtypes.GPU{
				Units: rtypes.NewResourceValue(0),
			},
			Storage: rtypes.Volumes{
				rtypes.Storage{
					Quantity: rtypes.NewResourceValue(256 * unit.Mi),
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
			testutil.Hostname(t),
		},
	}
}
