package logging

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"go.uber.org/zap/zapcore"
)

// conformanceFixturePath is the provider's tools/logging/testdata/conformance.json. The provider
// runs the same file against tools/logging, which this package mirrors, so a change made to one
// copy only fails the other's tests.
const conformanceFixturePath = "../../../../tools/logging/testdata/conformance.json"

type conformanceFixture struct {
	Defaults struct {
		Level     string `json:"level"`
		Format    string `json:"format"`
		Color     bool   `json:"color"`
		Timestamp string `json:"timestamp"`
		Trace     bool   `json:"trace"`
	} `json:"defaults"`
	Cases []struct {
		Name string `json:"name"`
		In   struct {
			Level     string `json:"level"`
			Format    string `json:"format"`
			Color     bool   `json:"color"`
			Timestamp string `json:"timestamp"`
			Trace     bool   `json:"trace"`
		} `json:"in"`
		// Want is nil when the input must be rejected.
		Want *conformanceWant `json:"want"`
		// Env holds the variables the provider generates from its configuration
		// (tools/logging Config.Env), without the ATTESTATION_ prefix; required with Want.
		Env map[string]string `json:"env"`
	} `json:"cases"`
}

type conformanceWant struct {
	LevelSpec string            `json:"level_spec"`
	Level     string            `json:"level"`
	Modules   map[string]string `json:"modules"`
	Format    string            `json:"format"`
	Color     bool              `json:"color"`
	Timestamp string            `json:"timestamp"`
	Trace     bool              `json:"trace"`
}

func loadConformanceFixture(t *testing.T) conformanceFixture {
	t.Helper()

	data, err := os.ReadFile(conformanceFixturePath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("conformance fixture not found: run the tests from the provider repository")
	}
	if err != nil {
		t.Fatal(err)
	}

	var fixture conformanceFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("conformance fixture has no cases")
	}

	return fixture
}

// levelName is the fixture's name of a resolved level: zap's name, or "disabled"
// for a level above every zap level.
func levelName(lvl zapcore.Level) string {
	if lvl > zapcore.FatalLevel {
		return "disabled"
	}

	return lvl.String()
}

func checkConformance(t *testing.T, want conformanceWant, cfg Config) {
	t.Helper()

	modules := map[string]string{}
	for module, lvl := range cfg.Modules {
		modules[module] = levelName(lvl)
	}

	wantModules := want.Modules
	if wantModules == nil {
		wantModules = map[string]string{}
	}

	if cfg.LevelSpec != want.LevelSpec {
		t.Errorf("LevelSpec = %q, want %q", cfg.LevelSpec, want.LevelSpec)
	}
	if got := levelName(cfg.Level); got != want.Level {
		t.Errorf("Level = %q, want %q", got, want.Level)
	}
	if !reflect.DeepEqual(modules, wantModules) {
		t.Errorf("Modules = %v, want %v", modules, wantModules)
	}
	if cfg.Format != want.Format {
		t.Errorf("Format = %q, want %q", cfg.Format, want.Format)
	}
	if cfg.Color != want.Color {
		t.Errorf("Color = %v, want %v", cfg.Color, want.Color)
	}
	if cfg.Timestamp != want.Timestamp {
		t.Errorf("Timestamp = %q, want %q", cfg.Timestamp, want.Timestamp)
	}
	if cfg.Trace != want.Trace {
		t.Errorf("Trace = %v, want %v", cfg.Trace, want.Trace)
	}
}

func TestDefaultsConformance(t *testing.T) {
	want := loadConformanceFixture(t).Defaults

	got := struct {
		Level, Format, Timestamp string
		Color, Trace             bool
	}{DefaultLevel, DefaultFormat, DefaultTimestamp, DefaultColor, DefaultTrace}

	if got.Level != want.Level || got.Format != want.Format || got.Timestamp != want.Timestamp ||
		got.Color != want.Color || got.Trace != want.Trace {
		t.Fatalf("defaults %+v differ from the provider's %+v", got, want)
	}
}

func TestParseConfigConformance(t *testing.T) {
	for _, tc := range loadConformanceFixture(t).Cases {
		t.Run(tc.Name, func(t *testing.T) {
			cfg, err := ParseConfig(tc.In.Level, tc.In.Format, tc.In.Color, tc.In.Timestamp, tc.In.Trace)
			if tc.Want == nil {
				if err == nil {
					t.Fatalf("accepted %+v, want an error", tc.In)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			checkConformance(t, *tc.Want, cfg)
			if tc.Env == nil {
				t.Fatal("accepted cases must list the variables the provider generates")
			}

			// the provider's attestation webhook injects the variables into the sidecar
			env := make(map[string]string, len(tc.Env))
			for name, val := range tc.Env {
				env["ATTESTATION_"+name] = val
			}

			cfg, err = ConfigFromEnv(envFrom(env))
			if err != nil {
				t.Fatalf("ConfigFromEnv(%v): %v", env, err)
			}
			checkConformance(t, *tc.Want, cfg)
		})
	}
}
