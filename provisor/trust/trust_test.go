package trust

import (
	"crypto/ed25519"
	"testing"
)

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return pub, priv
}

func sign(priv ed25519.PrivateKey, keyID string, payload []byte) Signature {
	return Signature{KeyID: keyID, Signature: ed25519.Sign(priv, payload)}
}

func twoKeySet(t *testing.T) (KeySet, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	pubA, privA := generateKey(t)
	pubB, privB := generateKey(t)
	return KeySet{Threshold: 2, Keys: map[string]ed25519.PublicKey{"a": pubA, "b": pubB}}, privA, privB
}

func TestKeySetVerifySatisfiedAtThreshold(t *testing.T) {
	ks, privA, privB := twoKeySet(t)
	payload := []byte(`{"hello":"world"}`)
	sigs := []Signature{sign(privA, "a", payload), sign(privB, "b", payload)}

	if got := ks.Verify(payload, sigs); got != Satisfied {
		t.Fatalf("Verify() = %v, want Satisfied", got)
	}
}

func TestKeySetVerifyThresholdNotMetWithOneSignature(t *testing.T) {
	ks, privA, _ := twoKeySet(t)
	payload := []byte(`{"hello":"world"}`)
	sigs := []Signature{sign(privA, "a", payload)}

	if got := ks.Verify(payload, sigs); got != ThresholdNotMet {
		t.Fatalf("Verify() = %v, want ThresholdNotMet", got)
	}
}

func TestKeySetVerifyUnknownKeyWhenNoClaimedKeyIsRecognized(t *testing.T) {
	ks, privA, _ := twoKeySet(t)
	payload := []byte(`{"hello":"world"}`)
	sigs := []Signature{{KeyID: "ghost", Signature: ed25519.Sign(privA, payload)}}

	if got := ks.Verify(payload, sigs); got != UnknownKey {
		t.Fatalf("Verify() = %v, want UnknownKey", got)
	}
}

func TestKeySetVerifyThresholdNotMetWhenOneSignatureIsForged(t *testing.T) {
	ks, _, privB := twoKeySet(t)
	_, foreignPriv := generateKey(t)
	payload := []byte(`{"hello":"world"}`)
	sigs := []Signature{
		{KeyID: "a", Signature: ed25519.Sign(foreignPriv, payload)},
		sign(privB, "b", payload),
	}

	if got := ks.Verify(payload, sigs); got != ThresholdNotMet {
		t.Fatalf("Verify() = %v, want ThresholdNotMet", got)
	}
}

func TestKeySetVerifyThresholdNotMetWhenSignatureCoversDifferentBytes(t *testing.T) {
	ks, privA, privB := twoKeySet(t)
	payload := []byte(`{"hello":"world"}`)
	sigs := []Signature{
		sign(privA, "a", []byte(`{"hello":"tampered"}`)),
		sign(privB, "b", payload),
	}

	if got := ks.Verify(payload, sigs); got != ThresholdNotMet {
		t.Fatalf("Verify() = %v, want ThresholdNotMet", got)
	}
}

func TestKeySetVerifyRejectsUndersizedPublicKeyWithoutPanicking(t *testing.T) {
	ks := KeySet{Threshold: 1, Keys: map[string]ed25519.PublicKey{"a": []byte("too-short")}}
	payload := []byte("payload")
	sigs := []Signature{{KeyID: "a", Signature: make([]byte, ed25519.SignatureSize)}}

	if got := ks.Verify(payload, sigs); got != ThresholdNotMet {
		t.Fatalf("Verify() = %v, want ThresholdNotMet", got)
	}
}

func TestRootsVerifySatisfiesOnAnyFile(t *testing.T) {
	pubA, privA := generateKey(t)
	payload := []byte("payload")
	roots := Roots{
		{Threshold: 2, Keys: map[string]ed25519.PublicKey{"x": pubA}},
		{Threshold: 1, Keys: map[string]ed25519.PublicKey{"a": pubA}},
	}
	sigs := []Signature{sign(privA, "a", payload)}

	outcome := roots.Verify(payload, sigs)
	if outcome != Satisfied {
		t.Fatalf("Verify() outcome = %v, want Satisfied", outcome)
	}
}

func TestRootsVerifyUnknownKeyWhenNoFileRecognizesAnyClaimedKey(t *testing.T) {
	_, privA := generateKey(t)
	pubB, _ := generateKey(t)
	payload := []byte("payload")
	roots := Roots{{Threshold: 1, Keys: map[string]ed25519.PublicKey{"b": pubB}}}
	sigs := []Signature{sign(privA, "a", payload)}

	outcome := roots.Verify(payload, sigs)
	if outcome != UnknownKey {
		t.Fatalf("Verify() outcome = %v, want UnknownKey", outcome)
	}
}
