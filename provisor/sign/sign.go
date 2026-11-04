// Package sign produces signed envelopes without knowing where a private key
// lives. The verifier is handed public keys and a threshold and cannot observe
// custody, so custody is a property of whoever publishes, not of the trust
// model. A Signer is the seam that keeps it that way: an envelope signed from
// a file, a cloud key service, or a hardware token is byte-identical.
package sign

import (
	"context"
	"crypto/ed25519"
	"fmt"

	"github.com/akash-network/provider/provisor/trust"
)

// Signer produces one signature over a payload under one named key.
type Signer interface {
	KeyID() string
	Sign(ctx context.Context, payload []byte) ([]byte, error)
}

// Envelope signs payload with every signer and returns the result.
//
// Duplicate key IDs are refused rather than deduplicated. A threshold counts
// distinct keys, so an envelope carrying the same ID twice can never satisfy a
// threshold above one, and producing it silently would push the failure all the
// way to a verifier that reports only ThresholdNotMet.
func Envelope(ctx context.Context, payload []byte, signers ...Signer) (trust.Envelope, error) {
	if len(signers) == 0 {
		return trust.Envelope{}, fmt.Errorf("sign: no signers")
	}

	seen := make(map[string]struct{}, len(signers))
	signatures := make([]trust.Signature, 0, len(signers))

	for _, signer := range signers {
		id := signer.KeyID()
		if _, duplicate := seen[id]; duplicate {
			return trust.Envelope{}, fmt.Errorf("sign: duplicate key id %q", id)
		}
		seen[id] = struct{}{}

		signature, err := signer.Sign(ctx, payload)
		if err != nil {
			return trust.Envelope{}, fmt.Errorf("sign: key %q: %w", id, err)
		}
		if len(signature) != ed25519.SignatureSize {
			return trust.Envelope{}, fmt.Errorf("sign: key %q returned %d bytes, want %d", id, len(signature), ed25519.SignatureSize)
		}

		signatures = append(signatures, trust.Signature{KeyID: id, Signature: signature})
	}

	return trust.Envelope{Payload: payload, Signatures: signatures}, nil
}
