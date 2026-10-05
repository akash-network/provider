package verify

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/trust"
)

func TestVerifyHappyPath(t *testing.T) {
	env := newTestEnv(t)

	result := env.verifier.Verify(context.Background(), env.input)

	if result.Reason != "" {
		t.Fatalf("Verify() Reason = %q, want empty", result.Reason)
	}
	if result.Manifest == nil {
		t.Fatalf("Verify() Manifest = nil, want a verified manifest")
	}
	if result.Manifest.Release != "1.2.0" {
		t.Fatalf("Manifest.Release = %q, want 1.2.0", result.Manifest.Release)
	}
}

func TestVerifyWrongSigningKeyOnKeySet(t *testing.T) {
	env := newTestEnv(t)
	_, foreignPriv := generateKey(t)

	payload := env.defaultKeySetPayload("2")
	sigs := []trust.Signature{
		{KeyID: "root-1", Signature: ed25519.Sign(foreignPriv, payload)},
		{KeyID: "root-2", Signature: ed25519.Sign(env.rootPriv2, payload)},
	}
	env.keySetBody = env.envelope(payload, sigs)

	assertReason(t, env, ReasonSignatureThresholdNotMet)
}

func TestVerifyManifestSignatureCoversDifferentBytes(t *testing.T) {
	env := newTestEnv(t)

	payload := env.defaultManifestPayload()
	tampered := append(append([]byte{}, payload...), '\n')
	sigs := []trust.Signature{
		{KeyID: "op-1", Signature: ed25519.Sign(env.opPriv1, tampered)},
		{KeyID: "op-2", Signature: ed25519.Sign(env.opPriv2, payload)},
	}
	env.setManifestBody(env.envelope(payload, sigs))

	assertReason(t, env, ReasonSignatureThresholdNotMet)
}

func TestVerifyChannelPointerThresholdNotMetWithOneSignature(t *testing.T) {
	env := newTestEnv(t)

	pointer := ChannelPointer{
		ManifestURL:    env.server.URL + "/manifest",
		ManifestDigest: digestOf(env.manifestBody),
	}
	payload, err := json.Marshal(pointer)
	if err != nil {
		t.Fatalf("json.Marshal(ChannelPointer): %v", err)
	}
	env.channelBody = env.envelope(payload, []trust.Signature{
		{KeyID: "op-1", Signature: ed25519.Sign(env.opPriv1, payload)},
	})

	assertReason(t, env, ReasonSignatureThresholdNotMet)
}

func TestVerifyUnknownSigningKeyOnManifest(t *testing.T) {
	env := newTestEnv(t)

	payload := env.defaultManifestPayload()
	sigs := []trust.Signature{
		{KeyID: "op-ghost-1", Signature: ed25519.Sign(env.opPriv1, payload)},
		{KeyID: "op-ghost-2", Signature: ed25519.Sign(env.opPriv2, payload)},
	}
	env.setManifestBody(env.envelope(payload, sigs))

	assertReason(t, env, ReasonUnknownSigningKey)
}

func TestVerifyKeySetExpired(t *testing.T) {
	env := newTestEnv(t)

	ks := release.KeySet{
		Version:   "2",
		ExpiresAt: env.now.Add(-time.Hour),
		Keys: []release.OperationalKey{
			{ID: "op-1", PublicKey: env.opPub1},
			{ID: "op-2", PublicKey: env.opPub2},
		},
	}
	payload, err := json.Marshal(ks)
	if err != nil {
		t.Fatalf("json.Marshal(KeySet): %v", err)
	}
	env.keySetBody = env.envelope(payload, env.signRoot(payload))

	assertReason(t, env, ReasonKeySetExpired)
}

func TestVerifyKeySetReplayed(t *testing.T) {
	env := newTestEnv(t)

	payload := env.defaultKeySetPayload("1") // equal to env.input.LastKeySetVersion
	env.keySetBody = env.envelope(payload, env.signRoot(payload))

	assertReason(t, env, ReasonKeySetReplayed)
}

func TestVerifyManifestDigestMismatch(t *testing.T) {
	env := newTestEnv(t)

	env.setChannelPointer(ChannelPointer{
		ManifestURL:    env.server.URL + "/manifest",
		ManifestDigest: "sha256:" + hex.EncodeToString(make([]byte, sha256.Size)),
	})

	assertReason(t, env, ReasonDigestMismatch)
}

func TestVerifyMalformedBase64InManifestEnvelope(t *testing.T) {
	env := newTestEnv(t)

	env.setManifestBody([]byte(`{"payload": "not-valid-base64!!!", "signatures": []}`))

	assertReason(t, env, ReasonMalformedDocument)
}

func TestVerifyMalformedJSONPayloadInManifest(t *testing.T) {
	env := newTestEnv(t)

	payload := []byte("not a json document")
	env.setManifestBody(env.envelope(payload, env.signOperational(payload)))

	assertReason(t, env, ReasonMalformedDocument)
}

func TestVerifySurfacesReleaseDecisionRefusals(t *testing.T) {
	env := newTestEnv(t)
	env.input.Installed.Release = "1.2.0" // equal to the manifest's Release: not monotonic

	assertReason(t, env, ReasonNotMonotonic)
}

func assertReason(t *testing.T, env *testEnv, want Reason) {
	t.Helper()
	result := env.verifier.Verify(context.Background(), env.input)
	if result.Reason != want {
		t.Fatalf("Verify() Reason = %q, want %q", result.Reason, want)
	}
	if result.Manifest != nil {
		t.Fatalf("Verify() Manifest = %+v, want nil", result.Manifest)
	}
}

func (env *testEnv) keySetPayloadWithThreshold(version release.Version, threshold int, keys []release.OperationalKey) []byte {
	env.t.Helper()
	payload, err := json.Marshal(release.KeySet{
		Version:   version,
		ExpiresAt: env.now.Add(time.Hour),
		Threshold: threshold,
		Keys:      keys,
	})
	if err != nil {
		env.t.Fatalf("json.Marshal(KeySet): %v", err)
	}
	return payload
}

func TestVerifyOperationalThresholdComesFromKeySetNotRoots(t *testing.T) {
	env := newTestEnv(t)
	env.verifier.Roots = trust.Roots{{
		Threshold: 1,
		Keys:      map[string]ed25519.PublicKey{"root-1": env.rootPub1},
	}}

	keySetPayload := env.defaultKeySetPayload("2")
	env.keySetBody = env.envelope(keySetPayload, []trust.Signature{
		{KeyID: "root-1", Signature: ed25519.Sign(env.rootPriv1, keySetPayload)},
	})

	manifestPayload := env.defaultManifestPayload()
	env.setManifestBody(env.envelope(manifestPayload, []trust.Signature{
		{KeyID: "op-1", Signature: ed25519.Sign(env.opPriv1, manifestPayload)},
	}))

	result := env.verifier.Verify(context.Background(), env.input)
	if result.Reason != ReasonSignatureThresholdNotMet {
		t.Fatalf("Verify() Reason = %q, want %q", result.Reason, ReasonSignatureThresholdNotMet)
	}
}

func TestVerifyKeySetThresholdExceedingItsOwnKeyCountIsMalformed(t *testing.T) {
	env := newTestEnv(t)
	payload := env.keySetPayloadWithThreshold("2", 3, []release.OperationalKey{
		{ID: "op-1", PublicKey: env.opPub1},
		{ID: "op-2", PublicKey: env.opPub2},
	})
	env.keySetBody = env.envelope(payload, env.signRoot(payload))

	result := env.verifier.Verify(context.Background(), env.input)
	if result.Reason != ReasonMalformedDocument {
		t.Fatalf("Verify() Reason = %q, want %q", result.Reason, ReasonMalformedDocument)
	}
}

func TestVerifyKeySetWithoutThresholdIsMalformed(t *testing.T) {
	env := newTestEnv(t)
	payload := env.keySetPayloadWithThreshold("2", 0, []release.OperationalKey{
		{ID: "op-1", PublicKey: env.opPub1},
	})
	env.keySetBody = env.envelope(payload, env.signRoot(payload))

	result := env.verifier.Verify(context.Background(), env.input)
	if result.Reason != ReasonMalformedDocument {
		t.Fatalf("Verify() Reason = %q, want %q", result.Reason, ReasonMalformedDocument)
	}
}
