package main

import (
	"reflect"
	"testing"
)

func TestStringSliceFlagDefaultsAlone(t *testing.T) {
	f := newStringSliceFlag("ghcr.io/akash-network")
	if got, want := f.values, []string{"ghcr.io/akash-network"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}

func TestStringSliceFlagSetClearsDefaults(t *testing.T) {
	f := newStringSliceFlag("ghcr.io/akash-network")
	if err := f.Set("ghcr.io/foo"); err != nil {
		t.Fatalf("Set(1): %v", err)
	}
	if err := f.Set("ghcr.io/bar"); err != nil {
		t.Fatalf("Set(2): %v", err)
	}
	if got, want := f.values, []string{"ghcr.io/foo", "ghcr.io/bar"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
}
