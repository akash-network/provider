package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/sign"
	"github.com/akash-network/provider/provisor/trust"
	"github.com/akash-network/provider/provisor/verify"
)

const (
	defaultRegistry           = "ghcr.io/akash-network"
	defaultRelease            = "0.18.0"
	defaultSupersedes         = "0.17.5"
	defaultMinUpgradableFrom  = "0.16.0"
	defaultKeySetVersion      = "2"
	defaultManifestSignatures = 2
	manifestLifetime          = 72 * time.Hour
	keySetLifetime            = 24 * time.Hour
)

// faultConfig names every document field devkit serve can perturb away from
// its default, happy-path value. defaultFaultConfig returns that baseline;
// every other value is produced by changing exactly one field away from it.
type faultConfig struct {
	Expired            bool
	Release            string
	MinUpgradableFrom  string
	SchemaVersion      int
	Unpinned           bool
	Registry           string
	ManifestSignatures int
	CorruptDigest      bool
	KeySetExpired      bool
	KeySetVersion      string
	UnknownSigner      bool
}

func defaultFaultConfig() faultConfig {
	return faultConfig{
		Release:            defaultRelease,
		MinUpgradableFrom:  defaultMinUpgradableFrom,
		SchemaVersion:      release.CurrentSchemaVersion,
		Registry:           defaultRegistry,
		ManifestSignatures: defaultManifestSignatures,
		KeySetVersion:      defaultKeySetVersion,
	}
}

type servedDocuments struct {
	KeySet   []byte
	Channel  []byte
	Manifest []byte
}

func buildDocuments(kb keyBundle, cfg faultConfig, manifestURL string, now time.Time) (servedDocuments, error) {
	manifestEnvelope, err := buildManifestEnvelope(kb, cfg, now)
	if err != nil {
		return servedDocuments{}, err
	}

	manifestDigest := sha256Digest(manifestEnvelope)
	if cfg.CorruptDigest {
		manifestDigest = sha256Digest(append(append([]byte{}, manifestEnvelope...), '!'))
	}

	channelEnvelope, err := buildChannelEnvelope(kb, manifestURL, manifestDigest)
	if err != nil {
		return servedDocuments{}, err
	}

	keySetEnvelope, err := buildKeySetEnvelope(kb, cfg, now)
	if err != nil {
		return servedDocuments{}, err
	}

	return servedDocuments{KeySet: keySetEnvelope, Channel: channelEnvelope, Manifest: manifestEnvelope}, nil
}

func buildManifestEnvelope(kb keyBundle, cfg faultConfig, now time.Time) ([]byte, error) {
	manifest := defaultManifest(cfg, now)
	payload, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}

	signers := kb.Operational
	if cfg.UnknownSigner {
		signers, err = ghostKeyPairs(len(kb.Operational))
		if err != nil {
			return nil, err
		}
	} else {
		count := min(max(cfg.ManifestSignatures, 0), len(signers))
		signers = signers[:count]
	}

	return signedEnvelope(payload, signers)
}

func defaultManifest(cfg faultConfig, now time.Time) release.Manifest {
	registry := cfg.Registry

	crdDigest := sha256Digest([]byte("akash-network-crds"))
	operatorChartDigest := sha256Digest([]byte("akash-provider-operator-chart"))
	operatorImageDigest := sha256Digest([]byte("akash-provider-operator-image"))
	providerChartDigest := sha256Digest([]byte("akash-provider-chart"))
	providerImageDigest := sha256Digest([]byte("akash-provider-image"))

	providerImage := release.RawReference(fmt.Sprintf("%s/provider:%s@%s", registry, cfg.Release, providerImageDigest))
	if cfg.Unpinned {
		providerImage = release.RawReference(fmt.Sprintf("%s/provider:%s", registry, cfg.Release))
	}

	expiresAt := now.Add(manifestLifetime)
	if cfg.Expired {
		expiresAt = now.Add(-time.Hour)
	}

	return release.Manifest{
		SchemaVersion:     cfg.SchemaVersion,
		Release:           release.Version(cfg.Release),
		Channel:           "stable",
		IssuedAt:          now,
		ExpiresAt:         expiresAt,
		Supersedes:        defaultSupersedes,
		MinUpgradableFrom: release.Version(cfg.MinUpgradableFrom),
		Rollout: []release.RolloutStage{
			{Cohorts: []int{0, 1, 2, 3}, NotBefore: now},
		},
		CRDs: []release.CRD{{
			Name:     "manifests.akash.network",
			Artifact: release.RawReference(fmt.Sprintf("oci://%s/crds/manifests@%s", registry, crdDigest)),
			Digest:   crdDigest,
		}},
		Components: []release.Component{
			{
				Name:      "akash-provider-operator",
				Order:     20,
				Release:   "akash-provider-operator",
				Namespace: "akash-services",
				Chart:     release.RawReference(fmt.Sprintf("oci://%s/charts/akash-provider-operator@%s", registry, operatorChartDigest)),
				Images: map[string]release.RawReference{
					"operator": release.RawReference(fmt.Sprintf("%s/operator:%s@%s", registry, cfg.Release, operatorImageDigest)),
				},
			},
			{
				Name:      "akash-provider",
				Order:     40,
				Release:   "akash-provider",
				Namespace: "akash-services",
				Chart:     release.RawReference(fmt.Sprintf("oci://%s/charts/akash-provider@%s", registry, providerChartDigest)),
				Images: map[string]release.RawReference{
					"provider": providerImage,
				},
			},
		},
		Notes: "generated by devkit serve",
	}
}

func buildChannelEnvelope(kb keyBundle, manifestURL, manifestDigest string) ([]byte, error) {
	pointer := verify.ChannelPointer{ManifestURL: manifestURL, ManifestDigest: manifestDigest}
	payload, err := json.Marshal(pointer)
	if err != nil {
		return nil, fmt.Errorf("marshaling channel pointer: %w", err)
	}
	return signedEnvelope(payload, kb.Operational)
}

func buildKeySetEnvelope(kb keyBundle, cfg faultConfig, now time.Time) ([]byte, error) {
	keys := make([]release.OperationalKey, len(kb.Operational))
	for i, k := range kb.Operational {
		keys[i] = release.OperationalKey{ID: k.ID, PublicKey: k.PublicKey}
	}

	expiresAt := now.Add(keySetLifetime)
	if cfg.KeySetExpired {
		expiresAt = now.Add(-time.Hour)
	}

	keySet := release.KeySet{
		Version:   release.Version(cfg.KeySetVersion),
		ExpiresAt: expiresAt,
		Threshold: len(kb.Operational),
		Keys:      keys,
	}
	payload, err := json.Marshal(keySet)
	if err != nil {
		return nil, fmt.Errorf("marshaling key set: %w", err)
	}
	return signedEnvelope(payload, kb.Roots)
}

// signedEnvelope goes through the same signing seam the real pipeline uses,
// so devkit exercises it rather than reimplementing it. The empty case is the
// exception: sign.Envelope rightly refuses to sign nothing, but
// --manifest-signatures=0 exists precisely to serve a document no verifier
// should accept, so that one is forged directly.
func signedEnvelope(payload []byte, pairs []keyPair) ([]byte, error) {
	if len(pairs) == 0 {
		return marshalEnvelope(trust.Envelope{Payload: payload})
	}

	signers := make([]sign.Signer, 0, len(pairs))
	for _, pair := range pairs {
		signer, err := sign.NewKey(pair.ID, pair.PrivateKey)
		if err != nil {
			return nil, err
		}
		signers = append(signers, signer)
	}

	envelope, err := sign.Envelope(context.Background(), payload, signers...)
	if err != nil {
		return nil, err
	}
	return marshalEnvelope(envelope)
}

func marshalEnvelope(envelope trust.Envelope) ([]byte, error) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshaling envelope: %w", err)
	}
	return data, nil
}

func ghostKeyPairs(n int) ([]keyPair, error) {
	pairs := make([]keyPair, n)
	for i := range pairs {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return nil, fmt.Errorf("generating ghost signer %d: %w", i+1, err)
		}
		pairs[i] = keyPair{ID: fmt.Sprintf("ghost-%d", i+1), PublicKey: pub, PrivateKey: priv}
	}
	return pairs, nil
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
