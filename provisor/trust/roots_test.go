package trust

import "testing"

func TestLoadParsesEmbeddedProductionRoots(t *testing.T) {
	roots, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("len(roots) = %d, want 1", len(roots))
	}
	if roots[0].Threshold != 2 {
		t.Fatalf("roots[0].Threshold = %d, want 2", roots[0].Threshold)
	}
	if len(roots[0].Keys) != 0 {
		t.Fatalf("len(roots[0].Keys) = %d, want 0", len(roots[0].Keys))
	}
}

func TestEmptyProductionRootsAcceptsNothing(t *testing.T) {
	roots, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	sigs := []Signature{{KeyID: "whoever", Signature: make([]byte, 64)}}
	if outcome := roots.Verify([]byte("anything"), sigs); outcome == Satisfied {
		t.Fatalf("Verify() = Satisfied, want not satisfied against an empty production root")
	}
	if outcome := roots.Verify([]byte("anything"), nil); outcome == Satisfied {
		t.Fatalf("Verify() with no signatures at all = Satisfied, want not satisfied")
	}
}
