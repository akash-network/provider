package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const devRootsPath = "trust/roots/dev.json"

type devRootKey struct {
	KeyID     string `json:"keyId"`
	PublicKey []byte `json:"publicKey"`
}

type devRootsDocument struct {
	Threshold int          `json:"threshold"`
	Keys      []devRootKey `json:"keys"`
}

func runKeys(args []string) error {
	fs := flag.NewFlagSet("devkit keys", flag.ContinueOnError)
	force := fs.Bool("force", false, "regenerate even if existing dev keys or trust roots would be overwritten")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*force {
		if _, err := os.Stat(devKeysPath); err == nil {
			return fmt.Errorf("%s already exists; pass --force to regenerate (this invalidates any binary already built against the current %s)", devKeysPath, devRootsPath)
		}
		if _, err := os.Stat(devRootsPath); err == nil {
			return fmt.Errorf("%s already exists; pass --force to regenerate (this invalidates any binary already built against the current %s)", devRootsPath, devRootsPath)
		}
	}

	kb, err := generateKeyBundle()
	if err != nil {
		return err
	}
	if err := kb.save(devKeysPath); err != nil {
		return err
	}
	if err := writeDevRoots(devRootsPath, kb); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", devKeysPath)
	fmt.Printf("wrote %s\n", devRootsPath)
	fmt.Println("rebuild devkit and provisor-fetch for the new roots to take effect")
	return nil
}

func writeDevRoots(path string, kb keyBundle) error {
	doc := devRootsDocument{Threshold: 2, Keys: make([]devRootKey, len(kb.Roots))}
	for i, k := range kb.Roots {
		doc.Keys[i] = devRootKey{KeyID: k.ID, PublicKey: k.PublicKey}
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
