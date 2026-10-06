package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("devkit serve", flag.ContinueOnError)
	// 8080 is deliberately avoided: _run/kube maps it on the host for the
	// gateway, so the default must not collide with the dev cluster this
	// harness is meant to run beside.
	addr := fs.String("addr", "127.0.0.1:8099", "address to listen on")
	// addr is also the only address devkit has for itself, so it is the
	// default for publicAddr too. The two diverge whenever the process
	// binds a wildcard or pod-local address (0.0.0.0, a pod IP) but is
	// reached by callers through a different name, e.g. a Kubernetes
	// Service's DNS name.
	publicAddr := fs.String("public-addr", "", "host:port embedded in served documents as the manifest's fetch URL (defaults to --addr)")

	cfg := defaultFaultConfig()
	fs.BoolVar(&cfg.Expired, "expired", cfg.Expired, "serve a manifest whose ExpiresAt is already in the past")
	fs.StringVar(&cfg.Release, "release", cfg.Release, "the manifest's release version")
	fs.StringVar(&cfg.MinUpgradableFrom, "min-upgradable-from", cfg.MinUpgradableFrom, "the manifest's minimum upgradable-from version")
	fs.IntVar(&cfg.SchemaVersion, "schema-version", cfg.SchemaVersion, "the manifest's schema version")
	fs.BoolVar(&cfg.Unpinned, "unpinned", cfg.Unpinned, "serve a provider image reference with no digest")
	fs.StringVar(&cfg.Registry, "registry", cfg.Registry, "the registry path all references are built under")
	fs.IntVar(&cfg.ManifestSignatures, "manifest-signatures", cfg.ManifestSignatures, "how many operational keys sign the manifest")
	fs.BoolVar(&cfg.CorruptDigest, "corrupt-digest", cfg.CorruptDigest, "serve a channel pointer whose manifest digest does not match")
	fs.BoolVar(&cfg.KeySetExpired, "keyset-expired", cfg.KeySetExpired, "serve a key-set whose ExpiresAt is already in the past")
	fs.StringVar(&cfg.KeySetVersion, "keyset-version", cfg.KeySetVersion, "the key-set's version")
	fs.BoolVar(&cfg.UnknownSigner, "unknown-signer", cfg.UnknownSigner, "sign the manifest with keys absent from the served key-set")

	if err := fs.Parse(args); err != nil {
		return err
	}

	kb, err := loadKeyBundle(devKeysPath)
	if err != nil {
		return fmt.Errorf("run `devkit keys` first: %w", err)
	}

	if *publicAddr == "" {
		*publicAddr = *addr
	}
	manifestURL := fmt.Sprintf("http://%s/manifest", *publicAddr)
	docs, err := buildDocuments(kb, cfg, manifestURL, time.Now())
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/keyset", serveDocument(docs.KeySet))
	mux.HandleFunc("/channel", serveDocument(docs.Channel))
	mux.HandleFunc("/manifest", serveDocument(docs.Manifest))

	fmt.Printf("http://%s/keyset\n", *addr)
	fmt.Printf("http://%s/channel\n", *addr)
	fmt.Printf("http://%s/manifest\n", *addr)

	if err := http.ListenAndServe(*addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

func serveDocument(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}
