package release

import (
	"strconv"
	"strings"
	"time"
)

// Decision is the outcome of validating a Manifest against an installed
// state: either Accepted, or one specific named refusal reason. A later
// phase surfaces each value as a distinct condition on a Kubernetes object,
// so refusals must never collapse into one generic failure.
type Decision string

const (
	Accepted           Decision = "Accepted"
	Expired            Decision = "Expired"
	NotMonotonic       Decision = "NotMonotonic"
	BelowUpgradeFloor  Decision = "BelowUpgradeFloor"
	UnsupportedSchema  Decision = "UnsupportedSchema"
	UnpinnedReference  Decision = "UnpinnedReference"
	DisallowedRegistry Decision = "DisallowedRegistry"
)

// InstalledState is what Validate needs to know about the provider it runs
// on: the release currently installed, and the registry paths the operator
// permits a reference to come from. Entries in AllowedRegistries are
// organisation paths, such as "ghcr.io/akash-network", never bare registry
// hosts - a bare host would admit any path under it.
type InstalledState struct {
	Release           Version
	AllowedRegistries []string
}

// Validate decides whether m may be applied over state as of now. It
// performs no I/O and reads no clock: now is supplied by the caller, which
// is what makes expiry deterministic to test. Signature verification is not
// part of this decision.
func Validate(m *Manifest, state InstalledState, now time.Time) Decision {
	if now.After(m.ExpiresAt) {
		return Expired
	}
	if m.Release.Compare(state.Release) <= 0 {
		return NotMonotonic
	}
	if state.Release.Compare(m.MinUpgradableFrom) < 0 {
		return BelowUpgradeFloor
	}
	if m.SchemaVersion != CurrentSchemaVersion {
		return UnsupportedSchema
	}

	parsed := make([]Reference, 0, len(m.Components)*2+len(m.CRDs))
	for _, raw := range manifestReferences(m) {
		ref, err := ParseReference(raw)
		if err != nil {
			return UnpinnedReference
		}
		parsed = append(parsed, ref)
	}
	for _, ref := range parsed {
		if !registryAllowed(ref.Repository, state.AllowedRegistries) {
			return DisallowedRegistry
		}
	}
	return Accepted
}

func manifestReferences(m *Manifest) []RawReference {
	refs := make([]RawReference, 0, len(m.Components)*2+len(m.CRDs))
	for _, c := range m.CRDs {
		refs = append(refs, c.Artifact)
	}
	for _, c := range m.Components {
		refs = append(refs, c.Chart)
		for _, img := range c.Images {
			refs = append(refs, img)
		}
	}
	return refs
}

// registryAllowed matches repository against allowed by "/"-separated path
// segment, not by raw string prefix, so an allowed entry of
// "ghcr.io/akash-network" does not also match "ghcr.io/akash-network-evil".
func registryAllowed(repository string, allowed []string) bool {
	repoSegments := strings.Split(repository, "/")
	for _, entry := range allowed {
		entrySegments := strings.Split(entry, "/")
		if len(entrySegments) > len(repoSegments) {
			continue
		}
		match := true
		for i, seg := range entrySegments {
			if repoSegments[i] != seg {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// Compare orders versions numerically component by component, so 0.16.10 is
// greater than 0.16.9 even though the second component's string form sorts
// the other way lexically. A missing trailing component compares as zero,
// so "1.0" equals "1.0.0". A non-numeric component also compares as zero,
// rather than erroring, so a malformed component never wins a comparison it
// should not.
//
// Everything from the first "-" onward, inclusive, is a pre-release suffix
// and plays no part in ordering: "1.0.0-rc1" compares equal to "1.0.0". This
// means the monotonic and floor checks cannot be defeated by appending a
// pre-release tag; a release author who needs a pre-release ordered ahead of
// its base release must mint a new numeric component instead.
func (v Version) Compare(other Version) int {
	a, b := numericComponents(v), numericComponents(other)
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func numericComponents(v Version) []int {
	s := string(v)
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out[i] = n
		}
	}
	return out
}
