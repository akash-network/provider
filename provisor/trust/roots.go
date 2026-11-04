package trust

import (
	"crypto/ed25519"
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed roots/*.json
var rootsFS embed.FS

// Roots is the compiled-in trust anchor: one KeySet per embedded roots file.
// A payload is trusted at the root tier if it satisfies any one file's own
// threshold against that same file's own keys; files are never combined to
// reach a threshold neither names on its own. This is what lets a dev build
// add roots/dev.json, with its own keys and threshold, alongside the tracked
// roots/production.json without editing it.
type Roots []KeySet

type rootKey struct {
	KeyID     string            `json:"keyId"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
}

type rootsDocument struct {
	Threshold int       `json:"threshold"`
	Keys      []rootKey `json:"keys"`
}

// Load parses every *.json file embedded from roots/ into a Roots. It
// returns an error only for a file that fails to parse as JSON; an empty
// keys array parses fine and yields a KeySet that Verify can never satisfy.
func Load() (Roots, error) {
	entries, err := rootsFS.ReadDir("roots")
	if err != nil {
		return nil, fmt.Errorf("trust: reading embedded roots directory: %w", err)
	}

	roots := make(Roots, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := rootsFS.ReadFile("roots/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("trust: reading %s: %w", entry.Name(), err)
		}

		var doc rootsDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("trust: parsing %s: %w", entry.Name(), err)
		}

		keys := make(map[string]ed25519.PublicKey, len(doc.Keys))
		for _, k := range doc.Keys {
			keys[k.KeyID] = k.PublicKey
		}
		roots = append(roots, KeySet{Threshold: doc.Threshold, Keys: keys})
	}
	return roots, nil
}

// Verify reports whether sigs satisfies any KeySet in r. It returns
// UnknownKey only if no file in r recognized any of the claimed key IDs; if
// any file recognized a key but no file reached its threshold, it returns
// ThresholdNotMet.
func (r Roots) Verify(payload []byte, sigs []Signature) Outcome {
	best := UnknownKey
	for _, ks := range r {
		switch ks.Verify(payload, sigs) {
		case Satisfied:
			return Satisfied
		case ThresholdNotMet:
			best = ThresholdNotMet
		}
	}
	return best
}
