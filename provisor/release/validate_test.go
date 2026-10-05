package release

import (
	"testing"
	"time"
)

func newBaselineManifest() (*Manifest, InstalledState, time.Time) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := now.Add(365 * 24 * time.Hour)

	m := &Manifest{
		SchemaVersion:     CurrentSchemaVersion,
		Release:           "1.2.0",
		Channel:           "stable",
		IssuedAt:          now.Add(-24 * time.Hour),
		ExpiresAt:         expiresAt,
		Supersedes:        "1.1.0",
		MinUpgradableFrom: "1.0.0",
		SecurityFix:       false,
		CRDBreaking:       false,
		Rollout: []RolloutStage{
			{Cohorts: []int{0, 1}, NotBefore: now},
		},
		CRDs: []CRD{
			{
				Name:     "akash-provider-crd",
				Artifact: "oci://ghcr.io/akash-network/charts/akash-provider-crd@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				Digest:   "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			},
		},
		Components: []Component{
			{
				Name:      "akash-provider",
				Order:     1,
				Namespace: "akash-services",
				Chart:     "oci://ghcr.io/akash-network/charts/akash-provider@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				Images: map[string]RawReference{
					"provider": "ghcr.io/akash-network/provider:0.17.0@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				},
			},
			{
				Name:      "akash-operator-inventory",
				Order:     0,
				Namespace: "akash-services",
				Chart:     "oci://ghcr.io/akash-network/charts/akash-operator-inventory@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				Images: map[string]RawReference{
					"operator": "ghcr.io/akash-network/operator-inventory:0.5.0@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
				},
			},
		},
		Notes: "",
	}

	state := InstalledState{
		Release:           "1.1.0",
		AllowedRegistries: []string{"ghcr.io/akash-network"},
	}

	return m, state, now
}

func TestValidate_Accepted(t *testing.T) {
	m, state, now := newBaselineManifest()

	if got := Validate(m, state, now); got != Accepted {
		t.Fatalf("Validate() = %s, want %s", got, Accepted)
	}
}

func TestValidate_Expired(t *testing.T) {
	m, state, _ := newBaselineManifest()
	now := m.ExpiresAt.Add(24 * time.Hour)

	if got := Validate(m, state, now); got != Expired {
		t.Fatalf("Validate() = %s, want %s", got, Expired)
	}
}

func TestValidate_ExpiryBoundary(t *testing.T) {
	t.Run("now equal to expiresAt is not expired", func(t *testing.T) {
		m, state, _ := newBaselineManifest()

		if got := Validate(m, state, m.ExpiresAt); got != Accepted {
			t.Fatalf("Validate() = %s, want %s", got, Accepted)
		}
	})

	t.Run("one nanosecond past expiresAt is expired", func(t *testing.T) {
		m, state, _ := newBaselineManifest()

		if got := Validate(m, state, m.ExpiresAt.Add(time.Nanosecond)); got != Expired {
			t.Fatalf("Validate() = %s, want %s", got, Expired)
		}
	})
}

func TestValidate_NotMonotonic(t *testing.T) {
	t.Run("release equal to installed", func(t *testing.T) {
		m, state, now := newBaselineManifest()
		m.Release = state.Release

		if got := Validate(m, state, now); got != NotMonotonic {
			t.Fatalf("Validate() = %s, want %s", got, NotMonotonic)
		}
	})

	t.Run("release less than installed", func(t *testing.T) {
		m, state, now := newBaselineManifest()
		m.Release = "1.0.5"

		if got := Validate(m, state, now); got != NotMonotonic {
			t.Fatalf("Validate() = %s, want %s", got, NotMonotonic)
		}
	})
}

func TestValidate_BelowUpgradeFloor(t *testing.T) {
	m, state, now := newBaselineManifest()
	state.Release = "0.9.0"

	if got := Validate(m, state, now); got != BelowUpgradeFloor {
		t.Fatalf("Validate() = %s, want %s", got, BelowUpgradeFloor)
	}
}

func TestValidate_UnsupportedSchema(t *testing.T) {
	m, state, now := newBaselineManifest()
	m.SchemaVersion = 2

	if got := Validate(m, state, now); got != UnsupportedSchema {
		t.Fatalf("Validate() = %s, want %s", got, UnsupportedSchema)
	}
}

func TestValidate_UnpinnedReference(t *testing.T) {
	t.Run("chart reference without digest", func(t *testing.T) {
		m, state, now := newBaselineManifest()
		m.Components[0].Chart = "ghcr.io/akash-network/charts/akash-provider:1.0.0"

		if got := Validate(m, state, now); got != UnpinnedReference {
			t.Fatalf("Validate() = %s, want %s", got, UnpinnedReference)
		}
	})

	t.Run("image reference without digest", func(t *testing.T) {
		m, state, now := newBaselineManifest()
		m.Components[1].Images["operator"] = "ghcr.io/akash-network/operator-inventory:0.5.0"

		if got := Validate(m, state, now); got != UnpinnedReference {
			t.Fatalf("Validate() = %s, want %s", got, UnpinnedReference)
		}
	})
}

func TestValidate_DisallowedRegistry(t *testing.T) {
	m, state, now := newBaselineManifest()
	m.Components[0].Chart = "oci://evil.example.com/charts/akash-provider@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	if got := Validate(m, state, now); got != DisallowedRegistry {
		t.Fatalf("Validate() = %s, want %s", got, DisallowedRegistry)
	}
}

func TestValidate_RegistryNearMiss(t *testing.T) {
	m, state, now := newBaselineManifest()
	m.Components[0].Images["provider"] = "ghcr.io/akash-network-evil/provider:0.17.0@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	if got := Validate(m, state, now); got != DisallowedRegistry {
		t.Fatalf("Validate() = %s, want %s", got, DisallowedRegistry)
	}
}

func TestValidate_RegistryAllowsDeeperPathUnderEntry(t *testing.T) {
	m, state, now := newBaselineManifest()
	m.Components[0].Images["provider"] = "ghcr.io/akash-network/nested/provider:0.17.0@sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	if got := Validate(m, state, now); got != Accepted {
		t.Fatalf("Validate() = %s, want %s", got, Accepted)
	}
}

func TestValidate_EmptyAllowlistRefusesEverything(t *testing.T) {
	m, state, now := newBaselineManifest()
	state.AllowedRegistries = nil

	if got := Validate(m, state, now); got != DisallowedRegistry {
		t.Fatalf("Validate() = %s, want %s", got, DisallowedRegistry)
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b Version
		want int
	}{
		{"numeric beats lexical", "0.16.10", "0.16.9", 1},
		{"numeric beats lexical reverse", "0.16.9", "0.16.10", -1},
		{"missing trailing component", "1.0", "1.0.0", 0},
		{"missing trailing component reverse", "1.0.0", "1.0", 0},
		{"pre-release suffix ignored", "1.0.0-rc1", "1.0.0", 0},
		{"pre-release suffix ignored reverse", "1.0.0", "1.0.0-rc1", 0},
		{"major component wins", "2.0.0", "1.9.9", 1},
		{"major component wins reverse", "1.9.9", "2.0.0", -1},
		{"equal versions", "1.2.3", "1.2.3", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.compare(tc.b); got != tc.want {
				t.Fatalf("%q.compare(%q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
