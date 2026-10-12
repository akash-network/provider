package logging

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestParseConfigDefaults(t *testing.T) {
	require.Equal(t, Config{
		LevelSpec: "info",
		Level:     zapcore.InfoLevel,
		Format:    "plain",
		Color:     true,
		Timestamp: "rfc3339",
		Trace:     false,
	}, DefaultConfig())
}

func TestParseConfigTable(t *testing.T) {
	for _, format := range []string{"plain", "json"} {
		cfg, err := ParseConfig("info", format, false, "rfc3339", false)
		require.NoError(t, err, format)
		require.Equal(t, format, cfg.Format)
	}

	timestamps := map[string]string{
		"off":         "off",
		"":            "off",
		"rfc3339":     "rfc3339",
		"rfc3339nano": "rfc3339nano",
		"kitchen":     "kitchen",
	}
	for in, want := range timestamps {
		cfg, err := ParseConfig("info", "plain", true, in, false)
		require.NoError(t, err, in)
		require.Equal(t, want, cfg.Timestamp, in)
	}

	levels := map[string]zapcore.Level{
		"trace": zapcore.DebugLevel,
		"debug": zapcore.DebugLevel,
		"info":  zapcore.InfoLevel,
		"warn":  zapcore.WarnLevel,
		"error": zapcore.ErrorLevel,
		"fatal": zapcore.FatalLevel,
		"panic": zapcore.PanicLevel,
	}
	for in, want := range levels {
		cfg, err := ParseConfig(in, "plain", true, "rfc3339", false)
		require.NoError(t, err, in)
		require.Equal(t, want, cfg.Level, in)
		require.Empty(t, cfg.Modules, in)
		require.Equal(t, in, cfg.LevelSpec)
	}

	cfg, err := ParseConfig("info", "plain", true, "rfc3339", true)
	require.NoError(t, err)
	require.True(t, cfg.Trace)
}

func TestParseConfigJSONForcesNoColor(t *testing.T) {
	cfg, err := ParseConfig("info", "json", true, "rfc3339", false)
	require.NoError(t, err)
	require.False(t, cfg.Color)

	cfg, err = ParseConfig("info", "plain", true, "rfc3339", false)
	require.NoError(t, err)
	require.True(t, cfg.Color)
}

func TestParseConfigModuleFilter(t *testing.T) {
	cfg, err := ParseConfig("bidengine-order:debug,*:info", "plain", true, "rfc3339", false)
	require.NoError(t, err)
	require.Equal(t, zapcore.InfoLevel, cfg.Level)
	require.Equal(t, map[string]zapcore.Level{"bidengine-order": zapcore.DebugLevel}, cfg.Modules)
	require.Equal(t, "bidengine-order:debug,*:info", cfg.LevelSpec)

	// no "*" entry: modules not listed are not filtered (cosmos semantics)
	cfg, err = ParseConfig("a:debug", "plain", true, "rfc3339", false)
	require.NoError(t, err)
	require.Equal(t, zapcore.DebugLevel, cfg.Level)
	require.Equal(t, map[string]zapcore.Level{"a": zapcore.DebugLevel}, cfg.Modules)
}

func TestParseConfigInvalid(t *testing.T) {
	cases := []struct {
		name                    string
		level, format, ts, want string
	}{
		{"format text", "info", "text", "rfc3339", "plain|json"},
		{"timestamp iso", "info", "plain", "iso", "off|rfc3339|rfc3339nano|kitchen"},
		{"level loud", "loud", "plain", "rfc3339", "trace|debug|info|warn|error|fatal|panic"},
		{"duplicate module", "a:debug,a:info", "plain", "rfc3339", "duplicate"},
		{"empty module level", "a:", "plain", "rfc3339", "trace|debug|info|warn|error|fatal|panic"},
	}

	for _, tc := range cases {
		_, err := ParseConfig(tc.level, tc.format, true, tc.ts, false)
		require.Error(t, err, tc.name)
		require.Contains(t, err.Error(), tc.want, tc.name)
	}
}

func TestConfigEnv(t *testing.T) {
	require.Equal(t, []EnvVar{
		{Name: "ATTESTATION_LOG_LEVEL", Value: "info"},
		{Name: "ATTESTATION_LOG_FORMAT", Value: "plain"},
		{Name: "ATTESTATION_LOG_COLOR", Value: "true"},
		{Name: "ATTESTATION_LOG_TIMESTAMP", Value: "rfc3339"},
		{Name: "ATTESTATION_TRACE", Value: "false"},
	}, DefaultConfig().Env("ATTESTATION_"))
}

func TestConfigFromViper(t *testing.T) {
	v := viper.New()

	cfg, err := ConfigFromViper(v)
	require.NoError(t, err)
	require.Equal(t, DefaultConfig(), cfg)

	v.Set(FlagLogFormat, "json")
	v.Set(FlagLogColor, true)
	v.Set(FlagLogTimestamp, "kitchen")
	v.Set(FlagLogLevel, "debug")
	v.Set(FlagTrace, true)

	cfg, err = ConfigFromViper(v)
	require.NoError(t, err)
	require.Equal(t, "json", cfg.Format)
	require.False(t, cfg.Color)
	require.Equal(t, "kitchen", cfg.Timestamp)
	require.Equal(t, zapcore.DebugLevel, cfg.Level)
	require.True(t, cfg.Trace)
}
