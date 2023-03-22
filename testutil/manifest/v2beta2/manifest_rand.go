package v2beta2

import (
	"math"
	"math/rand"
	"testing"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	atestutil "github.com/akash-network/node/testutil"
)

// RandManifestGenerator generates a manifest with random values
var RandManifestGenerator Generator = manifestGeneratorRand{}

type manifestGeneratorRand struct{}

func (mg manifestGeneratorRand) Manifest(t testing.TB) manifest.Manifest {
	t.Helper()
	return []manifest.Group{
		mg.Group(t),
	}
}

func (mg manifestGeneratorRand) Group(t testing.TB) manifest.Group {
	t.Helper()
	return manifest.Group{
		Name: atestutil.Name(t, "manifest-group"),
		Services: []manifest.Service{
			mg.Service(t),
		},
	}
}

func (mg manifestGeneratorRand) Service(t testing.TB) manifest.Service {
	t.Helper()
	return manifest.Service{
		Name:      "demo",
		Image:     "quay.io/ovrclk/demo-app",
		Args:      []string{"run"},
		Env:       []string{"AKASH_TEST_SERVICE=true"},
		Resources: atestutil.ResourceUnits(t),
		Count:     rand.Uint32(), // nolint: gosec
		Expose: []manifest.ServiceExpose{
			mg.ServiceExpose(t),
		},
	}
}

func (mg manifestGeneratorRand) ServiceExpose(t testing.TB) manifest.ServiceExpose {
	return manifest.ServiceExpose{
		Port:         uint32(rand.Intn(math.MaxUint16)), // nolint: gosec
		ExternalPort: uint32(rand.Intn(math.MaxUint16)), // nolint: gosec
		Proto:        "TCP",
		Service:      "svc",
		Global:       true,
		Hosts: []string{
			atestutil.Hostname(t),
		},
	}
}
