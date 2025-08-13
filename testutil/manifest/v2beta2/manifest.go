package v2beta2

import (
	"testing"

	manifest "pkg.akt.dev/go/manifest/v2beta3"
)

var (
	// DefaultManifestGenerator is the default test manifest generator
	DefaultManifestGenerator = RandManifestGenerator

	// Generators is a list of all available manifest generators
	Generators = []struct {
		Name      string
		Generator Generator
	}{
		{"overflow", OverflowManifestGenerator},
		{"random", RandManifestGenerator},
		{"app", AppManifestGenerator},
	}
)

// Generator is an interface for generating test manifests
type Generator interface {
	Manifest(t testing.TB) manifest.Manifest
	Group(t testing.TB) manifest.Group
	Service(t testing.TB) manifest.Service
	ServiceExpose(t testing.TB) manifest.ServiceExpose
}
