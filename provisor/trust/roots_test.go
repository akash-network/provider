package trust

import (
	"encoding/json"
	"testing"
)

// These tests must hold whether or not a developer has generated
// roots/dev.json, since running the dev loop is exactly when they get run.
// They therefore assert properties of the committed production roots and of
// Load's behaviour, never the number of embedded files.

func TestCommittedProductionRootsNameNoKeys(t *testing.T) {
	data, err := rootsFS.ReadFile("roots/production.json")
	if err != nil {
		t.Fatalf("reading embedded production roots: %v", err)
	}

	var doc rootsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing embedded production roots: %v", err)
	}

	if doc.Threshold != 2 {
		t.Fatalf("production threshold = %d, want 2", doc.Threshold)
	}
	if len(doc.Keys) != 0 {
		t.Fatalf("production roots name %d keys, want 0 until real keys exist", len(doc.Keys))
	}
}

func TestLoadParsesEveryEmbeddedRootsFile(t *testing.T) {
	roots, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("Load() returned no key sets, want at least the committed production roots")
	}

	for i, ks := range roots {
		if ks.Threshold <= 0 {
			t.Fatalf("roots[%d].Threshold = %d, want a positive threshold", i, ks.Threshold)
		}
	}
}

func TestKeySetNamingNoKeysAcceptsNothing(t *testing.T) {
	empty := KeySet{Threshold: 2}

	sigs := []Signature{{KeyID: "whoever", Signature: make([]byte, 64)}}
	if outcome := empty.Verify([]byte("anything"), sigs); outcome == Satisfied {
		t.Fatal("Verify() = Satisfied against a key set naming no keys")
	}
	if outcome := empty.Verify([]byte("anything"), nil); outcome == Satisfied {
		t.Fatal("Verify() with no signatures at all = Satisfied")
	}
}

func TestLoadedRootsRejectSignaturesFromUnnamedKeys(t *testing.T) {
	roots, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	sigs := []Signature{{KeyID: "not-a-root", Signature: make([]byte, 64)}}
	if outcome := roots.Verify([]byte("anything"), sigs); outcome == Satisfied {
		t.Fatal("Verify() = Satisfied for a key no embedded roots file names")
	}
}
