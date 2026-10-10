// Package trust checks threshold Ed25519 signatures against named key sets.
// It has no knowledge of HTTP, release manifests, or any fetching concern;
// callers decode bytes from wherever they came from and hand them here.
package trust

import "crypto/ed25519"

// Signature is one claimed signature over an Envelope's Payload, naming the
// key that supposedly produced it.
type Signature struct {
	KeyID     string `json:"keyId"`
	Signature []byte `json:"signature"`
}

// Envelope is the wire shape shared by the key-set, the channel pointer, and
// the manifest: a document's JSON bytes carried as Payload, alongside the
// signatures claimed over exactly those bytes. encoding/json base64-decodes
// Payload and each Signature.Signature automatically, since both are typed
// []byte; nothing here re-serializes Payload before it is verified.
type Envelope struct {
	Payload    []byte      `json:"payload"`
	Signatures []Signature `json:"signatures"`
}

// Outcome is the result of checking an Envelope's signatures against a
// KeySet. Its zero value is ThresholdNotMet, so a KeySet or Outcome used
// before being populated fails closed rather than reading as trusted.
type Outcome int

const (
	ThresholdNotMet Outcome = iota
	UnknownKey
	Satisfied
)

// KeySet is a set of named public keys and the number of distinct, validly
// signed keys required to treat a payload as trusted.
type KeySet struct {
	Threshold int
	Keys      map[string]ed25519.PublicKey
}

// Verify reports whether sigs contains a Threshold of valid signatures over
// payload from distinct keys in k. A keyId repeated in sigs counts once. It
// returns UnknownKey only when none of the claimed key IDs are present in k
// at all, so a verifier can tell "nobody we recognize signed this" apart
// from "we recognized a signer, but not enough of them."
func (k KeySet) Verify(payload []byte, sigs []Signature) Outcome {
	if k.Threshold <= 0 {
		return ThresholdNotMet
	}

	satisfied := make(map[string]bool, len(sigs))
	anyKnownKey := false
	for _, s := range sigs {
		pub, ok := k.Keys[s.KeyID]
		if !ok {
			continue
		}
		anyKnownKey = true
		if len(pub) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(pub, payload, s.Signature) {
			satisfied[s.KeyID] = true
		}
	}

	if len(satisfied) >= k.Threshold {
		return Satisfied
	}
	if !anyKnownKey {
		return UnknownKey
	}
	return ThresholdNotMet
}
