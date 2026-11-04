package release

import (
	"encoding/json"
	"testing"
	"time"
)

func TestManifestUnmarshal(t *testing.T) {
	const doc = `{
		"schemaVersion": 1,
		"release": "1.3.0",
		"channel": "stable",
		"issuedAt": "2026-01-15T10:00:00Z",
		"expiresAt": "2026-04-15T10:00:00Z",
		"supersedes": "1.2.0",
		"minUpgradableFrom": "1.0.0",
		"securityFix": true,
		"crdBreaking": false,
		"rollout": [
			{"cohorts": [0, 1], "notBefore": "2026-01-16T00:00:00Z"},
			{"cohorts": [2, 3, 4], "notBefore": "2026-01-20T00:00:00Z"}
		],
		"crds": [
			{
				"name": "akash-provider-crd",
				"artifact": "oci://ghcr.io/akash-network/charts/akash-provider-crd@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				"digest": "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
			}
		],
		"components": [
			{
				"name": "akash-provider",
				"order": 1,
				"namespace": "akash-services",
				"chart": "oci://ghcr.io/akash-network/charts/akash-provider@sha256:cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
				"images": {
					"provider": "ghcr.io/akash-network/provider:0.17.0@sha256:5d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f00"
				}
			},
			{
				"name": "akash-operator-inventory",
				"order": 0,
				"namespace": "akash-services",
				"chart": "oci://ghcr.io/akash-network/charts/akash-operator-inventory@sha256:88de000088de000088de000088de000088de000088de000088de000088de00",
				"images": {
					"operator": "ghcr.io/akash-network/operator-inventory:0.5.0@sha256:feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"
				}
			}
		],
		"notes": "Security patch for CVE-XXXX; recommended for all providers."
	}`

	var m Manifest
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if m.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", m.SchemaVersion)
	}
	if m.Release != "1.3.0" {
		t.Errorf("Release = %q, want %q", m.Release, "1.3.0")
	}
	if m.Channel != "stable" {
		t.Errorf("Channel = %q, want %q", m.Channel, "stable")
	}

	wantIssuedAt, err := time.Parse(time.RFC3339, "2026-01-15T10:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse(issuedAt): %v", err)
	}
	if !m.IssuedAt.Equal(wantIssuedAt) {
		t.Errorf("IssuedAt = %v, want %v", m.IssuedAt, wantIssuedAt)
	}

	wantExpiresAt, err := time.Parse(time.RFC3339, "2026-04-15T10:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse(expiresAt): %v", err)
	}
	if !m.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", m.ExpiresAt, wantExpiresAt)
	}

	if m.Supersedes != "1.2.0" {
		t.Errorf("Supersedes = %q, want %q", m.Supersedes, "1.2.0")
	}
	if m.MinUpgradableFrom != "1.0.0" {
		t.Errorf("MinUpgradableFrom = %q, want %q", m.MinUpgradableFrom, "1.0.0")
	}
	if !m.SecurityFix {
		t.Errorf("SecurityFix = %v, want true", m.SecurityFix)
	}
	if m.CRDBreaking {
		t.Errorf("CRDBreaking = %v, want false", m.CRDBreaking)
	}

	if len(m.Rollout) != 2 {
		t.Fatalf("len(Rollout) = %d, want 2", len(m.Rollout))
	}

	wantStage0NotBefore, err := time.Parse(time.RFC3339, "2026-01-16T00:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse(rollout[0].notBefore): %v", err)
	}
	if got := m.Rollout[0].Cohorts; len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("Rollout[0].Cohorts = %v, want [0 1]", got)
	}
	if !m.Rollout[0].NotBefore.Equal(wantStage0NotBefore) {
		t.Errorf("Rollout[0].NotBefore = %v, want %v", m.Rollout[0].NotBefore, wantStage0NotBefore)
	}

	wantStage1NotBefore, err := time.Parse(time.RFC3339, "2026-01-20T00:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse(rollout[1].notBefore): %v", err)
	}
	if got := m.Rollout[1].Cohorts; len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Errorf("Rollout[1].Cohorts = %v, want [2 3 4]", got)
	}
	if !m.Rollout[1].NotBefore.Equal(wantStage1NotBefore) {
		t.Errorf("Rollout[1].NotBefore = %v, want %v", m.Rollout[1].NotBefore, wantStage1NotBefore)
	}

	if len(m.CRDs) != 1 {
		t.Fatalf("len(CRDs) = %d, want 1", len(m.CRDs))
	}
	crd := m.CRDs[0]
	if crd.Name != "akash-provider-crd" {
		t.Errorf("CRDs[0].Name = %q, want %q", crd.Name, "akash-provider-crd")
	}
	if crd.Artifact != "oci://ghcr.io/akash-network/charts/akash-provider-crd@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("CRDs[0].Artifact = %q, unexpected", crd.Artifact)
	}
	if crd.Digest != "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("CRDs[0].Digest = %q, unexpected", crd.Digest)
	}

	if len(m.Components) != 2 {
		t.Fatalf("len(Components) = %d, want 2", len(m.Components))
	}

	c0 := m.Components[0]
	if c0.Name != "akash-provider" {
		t.Errorf("Components[0].Name = %q, want %q", c0.Name, "akash-provider")
	}
	if c0.Order != 1 {
		t.Errorf("Components[0].Order = %d, want 1", c0.Order)
	}
	if c0.Namespace != "akash-services" {
		t.Errorf("Components[0].Namespace = %q, want %q", c0.Namespace, "akash-services")
	}
	if c0.Chart != "oci://ghcr.io/akash-network/charts/akash-provider@sha256:cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe" {
		t.Errorf("Components[0].Chart = %q, unexpected", c0.Chart)
	}
	if len(c0.Images) != 1 || c0.Images["provider"] != "ghcr.io/akash-network/provider:0.17.0@sha256:5d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f005d3c4f00" {
		t.Errorf("Components[0].Images = %v, unexpected", c0.Images)
	}

	c1 := m.Components[1]
	if c1.Name != "akash-operator-inventory" {
		t.Errorf("Components[1].Name = %q, want %q", c1.Name, "akash-operator-inventory")
	}
	if c1.Order != 0 {
		t.Errorf("Components[1].Order = %d, want 0", c1.Order)
	}
	if c1.Namespace != "akash-services" {
		t.Errorf("Components[1].Namespace = %q, want %q", c1.Namespace, "akash-services")
	}
	if c1.Chart != "oci://ghcr.io/akash-network/charts/akash-operator-inventory@sha256:88de000088de000088de000088de000088de000088de000088de000088de00" {
		t.Errorf("Components[1].Chart = %q, unexpected", c1.Chart)
	}
	if len(c1.Images) != 1 || c1.Images["operator"] != "ghcr.io/akash-network/operator-inventory:0.5.0@sha256:feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface" {
		t.Errorf("Components[1].Images = %v, unexpected", c1.Images)
	}

	if m.Notes != "Security patch for CVE-XXXX; recommended for all providers." {
		t.Errorf("Notes = %q, unexpected", m.Notes)
	}
}
