package release

import (
	"crypto/ed25519"
	"time"
)

// CurrentSchemaVersion is the only Manifest.SchemaVersion this build understands.
const CurrentSchemaVersion = 1

type Channel string

// Version is a release coordinate: a dotted sequence of numeric components,
// optionally followed by a "-" and a pre-release suffix. See the compare
// method in validate.go for the exact ordering semantics.
type Version string

type Manifest struct {
	SchemaVersion     int            `json:"schemaVersion"`
	Release           Version        `json:"release"`
	Channel           Channel        `json:"channel"`
	IssuedAt          time.Time      `json:"issuedAt"`
	ExpiresAt         time.Time      `json:"expiresAt"`
	Supersedes        Version        `json:"supersedes"`
	MinUpgradableFrom Version        `json:"minUpgradableFrom"`
	SecurityFix       bool           `json:"securityFix"`
	CRDBreaking       bool           `json:"crdBreaking"`
	Rollout           []RolloutStage `json:"rollout"`
	CRDs              []CRD          `json:"crds"`
	Components        []Component    `json:"components"`
	Notes             string         `json:"notes"`
}

// RolloutStage permits the listed provider cohorts to apply the release from
// NotBefore onward. A manifest carries its own cohort schedule because there
// is no central rollout orchestrator; each provider computes its own earliest
// apply time from its own cohort.
type RolloutStage struct {
	Cohorts   []int     `json:"cohorts"`
	NotBefore time.Time `json:"notBefore"`
}

// CRD is one CustomResourceDefinition artifact the daemon applies directly,
// because Helm does not upgrade CRDs.
type CRD struct {
	Name     string       `json:"name"`
	Artifact RawReference `json:"artifact"`
	Digest   string       `json:"digest"`
}

// Component is one Helm release the daemon installs or upgrades. Order is
// relative to the manifest's other components (the provider blocks on its
// operators, so operators order before the provider). Release is the Helm
// release name in the cluster, which the apply phase upgrades by name and
// which the daemon's role scopes to; it is deliberately separate from Name,
// the manifest's own identifier for the component.
type Component struct {
	Name      string                  `json:"name"`
	Order     int                     `json:"order"`
	Release   string                  `json:"release"`
	Namespace string                  `json:"namespace"`
	Chart     RawReference            `json:"chart"`
	Images    map[string]RawReference `json:"images"`
}

// KeySet names the operational signing keys currently authorised to sign
// release manifests. It carries the same ExpiresAt and monotonic Version
// fields as a Manifest, so an old key set naming a since-revoked key cannot
// be replayed against a verifier that only checks the root signature.
//
// Threshold is the operational policy and is deliberately stated here rather
// than inherited from the root key set that signed this document. The two
// tiers are rotated independently, and a root set with more keys than the
// operational set would otherwise impose a threshold the operational keys
// could never satisfy.
type KeySet struct {
	Version   Version          `json:"version"`
	ExpiresAt time.Time        `json:"expiresAt"`
	Threshold int              `json:"threshold"`
	Keys      []OperationalKey `json:"keys"`
}

// OperationalKey is one signing key named by a KeySet.
type OperationalKey struct {
	ID        string            `json:"id"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
}
