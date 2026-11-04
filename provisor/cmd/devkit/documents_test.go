package main

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/trust"
	"github.com/akash-network/provider/provisor/verify"
)

// devkitTestEnv is a full set of served documents built from a freshly
// generated key bundle, backed by an in-memory httptest server. It never
// touches devKeysPath or devRootsPath.
type devkitTestEnv struct {
	t      *testing.T
	kb     keyBundle
	now    time.Time
	server *httptest.Server
	docs   servedDocuments
}

func newDevkitTestEnv(t *testing.T) *devkitTestEnv {
	t.Helper()

	kb, err := generateKeyBundle()
	if err != nil {
		t.Fatalf("generateKeyBundle: %v", err)
	}

	env := &devkitTestEnv{t: t, kb: kb, now: time.Now()}

	mux := http.NewServeMux()
	mux.HandleFunc("/keyset", func(w http.ResponseWriter, r *http.Request) { w.Write(env.docs.KeySet) })
	mux.HandleFunc("/channel", func(w http.ResponseWriter, r *http.Request) { w.Write(env.docs.Channel) })
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) { w.Write(env.docs.Manifest) })
	env.server = httptest.NewServer(mux)
	t.Cleanup(env.server.Close)

	return env
}

func (env *devkitTestEnv) build(cfg faultConfig) {
	env.t.Helper()
	docs, err := buildDocuments(env.kb, cfg, env.server.URL+"/manifest", env.now)
	if err != nil {
		env.t.Fatalf("buildDocuments: %v", err)
	}
	env.docs = docs
}

func (env *devkitTestEnv) verifier() *verify.Verifier {
	keys := make(map[string]ed25519.PublicKey, len(env.kb.Roots))
	for _, k := range env.kb.Roots {
		keys[k.ID] = k.PublicKey
	}
	return &verify.Verifier{
		Client: env.server.Client(),
		Now:    func() time.Time { return env.now },
		Roots:  trust.Roots{{Threshold: 2, Keys: keys}},
	}
}

func (env *devkitTestEnv) input() verify.Input {
	return verify.Input{
		KeySetURL:         env.server.URL + "/keyset",
		ChannelURL:        env.server.URL + "/channel",
		LastKeySetVersion: "1",
		Installed: release.InstalledState{
			Release:           "0.17.5",
			AllowedRegistries: []string{defaultRegistry},
		},
	}
}

func TestDevkitHappyPathVerifies(t *testing.T) {
	env := newDevkitTestEnv(t)
	env.build(defaultFaultConfig())

	result := env.verifier().Verify(context.Background(), env.input())
	if result.Reason != "" {
		t.Fatalf("Reason = %q, want empty", result.Reason)
	}
	if result.Manifest == nil {
		t.Fatalf("Manifest = nil, want non-nil")
	}
	if result.Manifest.Release != defaultRelease {
		t.Fatalf("Manifest.Release = %q, want %q", result.Manifest.Release, defaultRelease)
	}
}

func TestDevkitFaultInjectionFlags(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*faultConfig)
		reason verify.Reason
	}{
		{"expired", func(cfg *faultConfig) { cfg.Expired = true }, verify.ReasonExpired},
		{"release at installed floor", func(cfg *faultConfig) { cfg.Release = "0.17.5" }, verify.ReasonNotMonotonic},
		{"min-upgradable-from above installed", func(cfg *faultConfig) { cfg.MinUpgradableFrom = "0.18.0" }, verify.ReasonBelowUpgradeFloor},
		{"schema-version", func(cfg *faultConfig) { cfg.SchemaVersion = 2 }, verify.ReasonUnsupportedSchema},
		{"unpinned", func(cfg *faultConfig) { cfg.Unpinned = true }, verify.ReasonUnpinnedReference},
		{"registry", func(cfg *faultConfig) { cfg.Registry = "ghcr.io/evil-example" }, verify.ReasonDisallowedRegistry},
		{"manifest-signatures", func(cfg *faultConfig) { cfg.ManifestSignatures = 1 }, verify.ReasonSignatureThresholdNotMet},
		{"corrupt-digest", func(cfg *faultConfig) { cfg.CorruptDigest = true }, verify.ReasonDigestMismatch},
		{"keyset-expired", func(cfg *faultConfig) { cfg.KeySetExpired = true }, verify.ReasonKeySetExpired},
		{"keyset-version", func(cfg *faultConfig) { cfg.KeySetVersion = "1" }, verify.ReasonKeySetReplayed},
		{"unknown-signer", func(cfg *faultConfig) { cfg.UnknownSigner = true }, verify.ReasonUnknownSigningKey},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDevkitTestEnv(t)
			cfg := defaultFaultConfig()
			tc.mutate(&cfg)
			env.build(cfg)

			result := env.verifier().Verify(context.Background(), env.input())
			if result.Reason != tc.reason {
				t.Fatalf("Reason = %q, want %q", result.Reason, tc.reason)
			}
			if result.Manifest != nil {
				t.Fatalf("Manifest = %+v, want nil", result.Manifest)
			}
		})
	}
}
