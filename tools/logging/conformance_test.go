package logging

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// conformanceFixture is testdata/conformance.json. The attestation sidecar, a separate module
// with its own copy of this package (sidecar/attestation/internal/logging), runs the same file
// against its implementation, so a change made to one copy only fails the other's tests.
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
		Want *struct {
			LevelSpec string            `json:"level_spec"`
			Level     string            `json:"level"`
			Modules   map[string]string `json:"modules"`
			Format    string            `json:"format"`
			Color     bool              `json:"color"`
			Timestamp string            `json:"timestamp"`
			Trace     bool              `json:"trace"`
		} `json:"want"`
	} `json:"cases"`
}

func loadConformanceFixture(t *testing.T) conformanceFixture {
	t.Helper()

	data, err := os.ReadFile("testdata/conformance.json")
	require.NoError(t, err)

	var fixture conformanceFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.NotEmpty(t, fixture.Cases)

	return fixture
}

func TestDefaultsConformance(t *testing.T) {
	want := loadConformanceFixture(t).Defaults

	require.Equal(t, want.Level, DefaultLevel)
	require.Equal(t, want.Format, DefaultFormat)
	require.Equal(t, want.Color, DefaultColor)
	require.Equal(t, want.Timestamp, DefaultTimestamp)
	require.Equal(t, want.Trace, DefaultTrace)
}

func TestParseConfigConformance(t *testing.T) {
	for _, tc := range loadConformanceFixture(t).Cases {
		t.Run(tc.Name, func(t *testing.T) {
			cfg, err := ParseConfig(tc.In.Level, tc.In.Format, tc.In.Color, tc.In.Timestamp, tc.In.Trace)
			if tc.Want == nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			modules := map[string]string{}
			for module, lvl := range cfg.Modules {
				modules[module] = lvl.String()
			}

			wantModules := tc.Want.Modules
			if wantModules == nil {
				wantModules = map[string]string{}
			}

			require.Equal(t, tc.Want.LevelSpec, cfg.LevelSpec)
			require.Equal(t, tc.Want.Level, cfg.Level.String())
			require.Equal(t, wantModules, modules)
			require.Equal(t, tc.Want.Format, cfg.Format)
			require.Equal(t, tc.Want.Color, cfg.Color)
			require.Equal(t, tc.Want.Timestamp, cfg.Timestamp)
			require.Equal(t, tc.Want.Trace, cfg.Trace)
		})
	}
}
