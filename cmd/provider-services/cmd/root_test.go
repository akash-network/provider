package cmd

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/akash-network/provider/tools/logging"
)

func clearLogEnv(t *testing.T) {
	t.Helper()
	for _, prefix := range []string{"AP_", "AKASH_"} {
		for _, name := range []string{"LOG_LEVEL", "LOG_FORMAT", "LOG_COLOR", "LOG_TIMESTAMP", "TRACE"} {
			t.Setenv(prefix+name, "")
		}
	}
}

// executeProbe runs the real root command with a hidden probe subcommand and
// returns the logging configuration the probe observed.
func executeProbe(t *testing.T, args ...string) (logging.Config, error) {
	t.Helper()
	return executeProbeWithHome(t, t.TempDir(), args...)
}

func executeProbeWithHome(t *testing.T, home string, args ...string) (logging.Config, error) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)

	var got logging.Config
	var ok bool

	root := NewRootCmd()
	root.AddCommand(&cobra.Command{
		Use:    "logprobe",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			got, ok = logging.ConfigFromCtx(cmd.Context())
			return nil
		},
	})
	root.SilenceErrors = true
	root.SetArgs(append([]string{"logprobe", "--home", home}, args...))

	err := ExecuteWithCtx(context.Background(), root, "AP")
	if err == nil {
		require.True(t, ok, "logger configuration not found in command context")
	}

	return got, err
}

func TestRootDefaults(t *testing.T) {
	clearLogEnv(t)

	got, err := executeProbe(t)
	require.NoError(t, err)
	require.Equal(t, logging.DefaultConfig(), got)
}

func TestRootLogOffAccepted(t *testing.T) {
	clearLogEnv(t)

	got, err := executeProbe(t, "--log_timestamp", "off")
	require.NoError(t, err)
	require.Equal(t, logging.TimestampOff, got.Timestamp)
}

func TestRootLogOffAcceptedFromAkashEnv(t *testing.T) {
	clearLogEnv(t)
	t.Setenv("AKASH_LOG_TIMESTAMP", "off")

	got, err := executeProbe(t)
	require.NoError(t, err)
	require.Equal(t, logging.TimestampOff, got.Timestamp)
}

func TestRootLogOptionsFromEnv(t *testing.T) {
	clearLogEnv(t)
	t.Setenv("AP_LOG_FORMAT", "json")
	t.Setenv("AP_LOG_COLOR", "true")
	t.Setenv("AP_LOG_LEVEL", "debug")

	got, err := executeProbe(t)
	require.NoError(t, err)
	require.Equal(t, logging.FormatJSON, got.Format)
	require.False(t, got.Color)
	require.Equal(t, "debug", got.LevelSpec)
}

// Levels accepted before the logging rework: "disabled" and zerolog's numeric levels,
// globally or for a module.
func TestRootPreviousLogLevelsAccepted(t *testing.T) {
	clearLogEnv(t)

	for _, level := range []string{"disabled", "provider:disabled,*:info", "1", "provider:1,*:info"} {
		got, err := executeProbe(t, "--log_level", level)
		require.NoError(t, err, level)
		require.Equal(t, level, got.LevelSpec, level)
	}
}

func TestNoTopLevelOperatorCommands(t *testing.T) {
	root := NewRootCmd()

	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.False(t, names["ip"], "top-level ip command must not exist")
	require.False(t, names["hostname"], "top-level hostname command must not exist")

	for _, sub := range []string{"ip", "hostname", "inventory"} {
		c, _, err := root.Find([]string{"operator", sub})
		require.NoError(t, err)
		require.Equal(t, sub, c.Name())
	}
}

// homeWithLogConfig creates a provider home with the generated config.toml whose
// top-level logging keys are replaced by values.
func homeWithLogConfig(t *testing.T, values map[string]string) string {
	t.Helper()

	home := t.TempDir()
	_, err := executeProbeWithHome(t, home)
	require.NoError(t, err)

	path := filepath.Join(home, "config", "config.toml")
	data, err := os.ReadFile(path) // nolint: gosec
	require.NoError(t, err)

	content := string(data)
	var top []string
	for key, val := range values {
		re := regexp.MustCompile(`(?m)^` + key + ` = .*$`)
		line := key + ` = "` + val + `"`
		if key == logging.FlagLogColor || key == logging.FlagTrace {
			line = key + " = " + val
		}
		if re.MatchString(content) {
			content = re.ReplaceAllString(content, line)
		} else {
			top = append(top, line)
		}
	}
	content = strings.Join(top, "\n") + "\n" + content

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600)) // nolint: gosec

	return home
}

func TestRootLogOptionsFromConfigFile(t *testing.T) {
	clearLogEnv(t)
	home := homeWithLogConfig(t, map[string]string{
		logging.FlagLogFormat:    "json",
		logging.FlagLogTimestamp: "kitchen",
		logging.FlagLogLevel:     "debug",
	})

	got, err := executeProbeWithHome(t, home)
	require.NoError(t, err)
	require.Equal(t, logging.FormatJSON, got.Format)
	require.Equal(t, logging.TimestampKitchen, got.Timestamp)
	require.Equal(t, "debug", got.LevelSpec)
}

func TestRootLogOffFromConfigFile(t *testing.T) {
	clearLogEnv(t)
	home := homeWithLogConfig(t, map[string]string{logging.FlagLogTimestamp: "off"})

	got, err := executeProbeWithHome(t, home)
	require.NoError(t, err)
	require.Equal(t, logging.TimestampOff, got.Timestamp)
}

func TestRootLogPrecedence(t *testing.T) {
	clearLogEnv(t)
	home := homeWithLogConfig(t, map[string]string{
		logging.FlagLogFormat:    "json",
		logging.FlagLogTimestamp: "kitchen",
		logging.FlagLogLevel:     "error",
	})

	// env beats config file, AP_ beats AKASH_, flag beats env
	t.Setenv("AKASH_LOG_TIMESTAMP", "rfc3339")
	t.Setenv("AP_LOG_TIMESTAMP", "rfc3339nano")
	t.Setenv("AKASH_LOG_LEVEL", "warn")
	t.Setenv("AP_LOG_FORMAT", "json")

	got, err := executeProbeWithHome(t, home, "--log_format", "plain")
	require.NoError(t, err)
	require.Equal(t, logging.FormatPlain, got.Format, "flag must beat AP_ env and config file")
	require.Equal(t, logging.TimestampRFC3339Nano, got.Timestamp, "AP_ env must beat AKASH_ env and config file")
	require.Equal(t, "warn", got.LevelSpec, "AKASH_ env must beat config file")
}
