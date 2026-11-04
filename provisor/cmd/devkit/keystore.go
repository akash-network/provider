package main

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const devKeysPath = ".devkeys/keys.json"

// keyPair is one generated Ed25519 keypair as devkit persists it: both
// halves live in the same untracked file, since .devkeys/ as a whole is the
// private material and nothing inside it is ever committed.
type keyPair struct {
	ID         string             `json:"id"`
	PublicKey  ed25519.PublicKey  `json:"publicKey"`
	PrivateKey ed25519.PrivateKey `json:"privateKey"`
}

type keyBundle struct {
	Roots       []keyPair `json:"roots"`
	Operational []keyPair `json:"operational"`
}

func generateKeyBundle() (keyBundle, error) {
	roots, err := generateKeyPairs("root", 2)
	if err != nil {
		return keyBundle{}, err
	}
	operational, err := generateKeyPairs("op", 2)
	if err != nil {
		return keyBundle{}, err
	}
	return keyBundle{Roots: roots, Operational: operational}, nil
}

func generateKeyPairs(prefix string, n int) ([]keyPair, error) {
	pairs := make([]keyPair, n)
	for i := range pairs {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return nil, fmt.Errorf("generating %s-%d keypair: %w", prefix, i+1, err)
		}
		pairs[i] = keyPair{ID: fmt.Sprintf("%s-%d", prefix, i+1), PublicKey: pub, PrivateKey: priv}
	}
	return pairs, nil
}

func (kb keyBundle) save(path string) error {
	data, err := json.MarshalIndent(kb, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling key bundle: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func loadKeyBundle(path string) (keyBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return keyBundle{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var kb keyBundle
	if err := json.Unmarshal(data, &kb); err != nil {
		return keyBundle{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return kb, nil
}
