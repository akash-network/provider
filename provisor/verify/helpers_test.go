package verify

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/trust"
)

func generateKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return pub, priv
}

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// testEnv is a full, happy-path chain: a two-of-two root key set trusting a
// two-of-two operational key set, which in turn signs a channel pointer and
// a manifest that reference each other correctly. Each test starts from
// this baseline and overwrites exactly the one document it means to break.
type testEnv struct {
	t        *testing.T
	now      time.Time
	server   *httptest.Server
	verifier *Verifier

	rootPriv1, rootPriv2 ed25519.PrivateKey
	rootPub1, rootPub2   ed25519.PublicKey
	opPriv1, opPriv2     ed25519.PrivateKey
	opPub1, opPub2       ed25519.PublicKey

	keySetBody   []byte
	channelBody  []byte
	manifestBody []byte

	input Input
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{t: t, now: time.Now()}

	env.rootPub1, env.rootPriv1 = generateKey(t)
	env.rootPub2, env.rootPriv2 = generateKey(t)
	env.opPub1, env.opPriv1 = generateKey(t)
	env.opPub2, env.opPriv2 = generateKey(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/keyset", func(w http.ResponseWriter, r *http.Request) { w.Write(env.keySetBody) })
	mux.HandleFunc("/channel", func(w http.ResponseWriter, r *http.Request) { w.Write(env.channelBody) })
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) { w.Write(env.manifestBody) })
	env.server = httptest.NewServer(mux)
	t.Cleanup(env.server.Close)

	env.verifier = &Verifier{
		Client: env.server.Client(),
		Now:    func() time.Time { return env.now },
		Roots: trust.Roots{{
			Threshold: 2,
			Keys:      map[string]ed25519.PublicKey{"root-1": env.rootPub1, "root-2": env.rootPub2},
		}},
	}

	manifestPayload := env.defaultManifestPayload()
	env.setManifestBody(env.envelope(manifestPayload, env.signOperational(manifestPayload)))

	keySetPayload := env.defaultKeySetPayload("2")
	env.keySetBody = env.envelope(keySetPayload, env.signRoot(keySetPayload))

	env.input = Input{
		KeySetURL:         env.server.URL + "/keyset",
		ChannelURL:        env.server.URL + "/channel",
		LastKeySetVersion: "1",
		Installed:         release.InstalledState{Release: "1.0.0", AllowedRegistries: []string{"ghcr.io/akash-network"}},
	}
	return env
}

func (env *testEnv) envelope(payload []byte, sigs []trust.Signature) []byte {
	env.t.Helper()
	body, err := json.Marshal(trust.Envelope{Payload: payload, Signatures: sigs})
	if err != nil {
		env.t.Fatalf("json.Marshal(Envelope): %v", err)
	}
	return body
}

func (env *testEnv) signRoot(payload []byte) []trust.Signature {
	return []trust.Signature{
		{KeyID: "root-1", Signature: ed25519.Sign(env.rootPriv1, payload)},
		{KeyID: "root-2", Signature: ed25519.Sign(env.rootPriv2, payload)},
	}
}

func (env *testEnv) signOperational(payload []byte) []trust.Signature {
	return []trust.Signature{
		{KeyID: "op-1", Signature: ed25519.Sign(env.opPriv1, payload)},
		{KeyID: "op-2", Signature: ed25519.Sign(env.opPriv2, payload)},
	}
}

func (env *testEnv) defaultKeySetPayload(version release.Version) []byte {
	env.t.Helper()
	ks := release.KeySet{
		Version:   version,
		ExpiresAt: env.now.Add(time.Hour),
		Threshold: 2,
		Keys: []release.OperationalKey{
			{ID: "op-1", PublicKey: env.opPub1},
			{ID: "op-2", PublicKey: env.opPub2},
		},
	}
	payload, err := json.Marshal(ks)
	if err != nil {
		env.t.Fatalf("json.Marshal(KeySet): %v", err)
	}
	return payload
}

func (env *testEnv) defaultManifestPayload() []byte {
	env.t.Helper()
	digest := "sha256:" + hex.EncodeToString(make([]byte, sha256.Size))
	m := release.Manifest{
		SchemaVersion:     release.CurrentSchemaVersion,
		Release:           "1.2.0",
		Channel:           "stable",
		IssuedAt:          env.now.Add(-time.Hour),
		ExpiresAt:         env.now.Add(24 * time.Hour),
		Supersedes:        "1.1.0",
		MinUpgradableFrom: "1.0.0",
		CRDs: []release.CRD{{
			Name:     "akash-network",
			Artifact: release.RawReference("oci://ghcr.io/akash-network/crds/akash-network@" + digest),
			Digest:   digest,
		}},
		Components: []release.Component{{
			Name:      "akash-provider",
			Order:     40,
			Release:   "akash-provider",
			Namespace: "akash-services",
			Chart:     release.RawReference("oci://ghcr.io/akash-network/charts/akash-provider@" + digest),
			Images: map[string]release.RawReference{
				"image": release.RawReference("ghcr.io/akash-network/provider:0.17.0@" + digest),
			},
		}},
	}
	payload, err := json.Marshal(m)
	if err != nil {
		env.t.Fatalf("json.Marshal(Manifest): %v", err)
	}
	return payload
}

// setManifestBody installs body as the bytes served at /manifest and points
// the channel pointer's digest at it, so any test that breaks only the
// manifest envelope does not also, incidentally, break the digest check.
func (env *testEnv) setManifestBody(body []byte) {
	env.manifestBody = body
	env.setChannelPointer(ChannelPointer{
		ManifestURL:    env.server.URL + "/manifest",
		ManifestDigest: digestOf(body),
	})
}

func (env *testEnv) setChannelPointer(pointer ChannelPointer) {
	env.t.Helper()
	payload, err := json.Marshal(pointer)
	if err != nil {
		env.t.Fatalf("json.Marshal(ChannelPointer): %v", err)
	}
	env.channelBody = env.envelope(payload, env.signOperational(payload))
}
