package akashnetwork

import _ "embed"

// go:embed cannot reach into a parent directory, so this lives next to
// crd.yaml rather than in migrations/.
//
//go:embed crd.yaml
var CRDManifest []byte
