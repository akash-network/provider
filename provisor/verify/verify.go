// Package verify runs the release supply chain's verification chain: key-set,
// then channel pointer, then manifest, each over HTTP, each checked against
// a threshold of signatures before its bytes are ever trusted enough to
// unmarshal. Every step fails closed with its own Reason; there is no way to
// construct a Verifier that skips a step.
package verify

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/trust"
)

// Verifier runs the chain. Client and Now are supplied by the caller rather
// than read from package state, so a verification run is reproducible and a
// test can substitute an httptest client and a fixed clock without touching
// any global.
type Verifier struct {
	Client *http.Client
	Now    func() time.Time
	Roots  trust.Roots
}

// Input names what one chain run resolves against: where to fetch the
// key-set and the channel pointer, the key-set version already on file (so a
// superseded key-set is refused as a replay), and the state release.Validate
// needs to judge the manifest it eventually reaches.
type Input struct {
	KeySetURL         string
	ChannelURL        string
	LastKeySetVersion release.Version
	Installed         release.InstalledState
}

// Verify runs the full chain and returns exactly one outcome: a verified
// Manifest, or a Result whose Reason names the first check that failed.
func (v *Verifier) Verify(ctx context.Context, in Input) Result {
	operational, reason := v.verifyKeySet(ctx, in.KeySetURL, in.LastKeySetVersion)
	if reason != "" {
		return Result{Reason: reason}
	}

	pointer, reason := v.verifyChannelPointer(ctx, in.ChannelURL, operational)
	if reason != "" {
		return Result{Reason: reason}
	}

	manifest, reason := v.verifyManifest(ctx, pointer, operational)
	if reason != "" {
		return Result{Reason: reason}
	}

	decision := release.Validate(manifest, in.Installed, v.Now())
	if decision != release.Accepted {
		return Result{Reason: Reason(decision)}
	}
	return Result{Manifest: manifest}
}

func (v *Verifier) verifyKeySet(ctx context.Context, url string, lastVersion release.Version) (trust.KeySet, Reason) {
	body, reason := fetch(ctx, v.Client, url, v.Now)
	if reason != "" {
		return trust.KeySet{}, reason
	}

	var env trust.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return trust.KeySet{}, ReasonMalformedDocument
	}

	if reason := reasonFromOutcome(v.Roots.Verify(env.Payload, env.Signatures)); reason != "" {
		return trust.KeySet{}, reason
	}

	var keySet release.KeySet
	if err := json.Unmarshal(env.Payload, &keySet); err != nil {
		return trust.KeySet{}, ReasonMalformedDocument
	}

	if v.Now().After(keySet.ExpiresAt) {
		return trust.KeySet{}, ReasonKeySetExpired
	}
	if keySet.Version.Compare(lastVersion) <= 0 {
		return trust.KeySet{}, ReasonKeySetReplayed
	}

	keys := operationalKeys(keySet)
	if keySet.Threshold <= 0 || keySet.Threshold > len(keys) {
		return trust.KeySet{}, ReasonMalformedDocument
	}

	return trust.KeySet{Threshold: keySet.Threshold, Keys: keys}, ""
}

func (v *Verifier) verifyChannelPointer(ctx context.Context, url string, operational trust.KeySet) (ChannelPointer, Reason) {
	body, reason := fetch(ctx, v.Client, url, v.Now)
	if reason != "" {
		return ChannelPointer{}, reason
	}

	var env trust.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ChannelPointer{}, ReasonMalformedDocument
	}

	if reason := reasonFromOutcome(operational.Verify(env.Payload, env.Signatures)); reason != "" {
		return ChannelPointer{}, reason
	}

	var pointer ChannelPointer
	if err := json.Unmarshal(env.Payload, &pointer); err != nil {
		return ChannelPointer{}, ReasonMalformedDocument
	}
	return pointer, ""
}

func (v *Verifier) verifyManifest(ctx context.Context, pointer ChannelPointer, operational trust.KeySet) (*release.Manifest, Reason) {
	body, reason := fetch(ctx, v.Client, pointer.ManifestURL, v.Now)
	if reason != "" {
		return nil, reason
	}

	if !digestMatches(body, pointer.ManifestDigest) {
		return nil, ReasonDigestMismatch
	}

	var env trust.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, ReasonMalformedDocument
	}

	if reason := reasonFromOutcome(operational.Verify(env.Payload, env.Signatures)); reason != "" {
		return nil, reason
	}

	var manifest release.Manifest
	if err := json.Unmarshal(env.Payload, &manifest); err != nil {
		return nil, ReasonMalformedDocument
	}
	return &manifest, ""
}

func reasonFromOutcome(outcome trust.Outcome) Reason {
	switch outcome {
	case trust.Satisfied:
		return ""
	case trust.UnknownKey:
		return ReasonUnknownSigningKey
	default:
		return ReasonSignatureThresholdNotMet
	}
}

func operationalKeys(keySet release.KeySet) map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey, len(keySet.Keys))
	for _, k := range keySet.Keys {
		keys[k.ID] = k.PublicKey
	}
	return keys
}

func digestMatches(body []byte, digest string) bool {
	const prefix = "sha256:"
	hexDigest, ok := strings.CutPrefix(digest, prefix)
	if !ok {
		return false
	}
	sum := sha256.Sum256(body)
	return strings.EqualFold(hexDigest, hex.EncodeToString(sum[:]))
}
