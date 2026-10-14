// Package logging provides the single logging setup used by all provider-services
// commands: option parsing, a zap-backed implementation of cosmossdk.io/log.Logger,
// cobra wiring and the canonical log keys.
package logging

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"go.uber.org/zap/zapcore"

	cflags "pkg.akt.dev/go/cli/flags"
)

// Option (flag / config key) names. Environment variables are derived by viper,
// e.g. AP_LOG_LEVEL, AP_LOG_FORMAT, AP_LOG_COLOR, AP_LOG_TIMESTAMP, AP_TRACE.
const (
	FlagLogLevel     = cflags.FlagLogLevel
	FlagLogFormat    = cflags.FlagLogFormat
	FlagLogColor     = cflags.FlagLogColor
	FlagLogTimestamp = cflags.FlagLogTimestamp
	FlagTrace        = cflags.FlagTrace
)

const (
	FormatPlain = "plain"
	FormatJSON  = "json"
)

const (
	TimestampOff         = "off"
	TimestampRFC3339     = "rfc3339"
	TimestampRFC3339Nano = "rfc3339nano"
	TimestampKitchen     = "kitchen"
)

const (
	DefaultLevel     = "info"
	DefaultFormat    = FormatPlain
	DefaultColor     = true
	DefaultTimestamp = TimestampRFC3339Nano
	DefaultTrace     = false
)

const (
	allowedFormats    = "plain|json"
	allowedTimestamps = "off|rfc3339|rfc3339nano|kitchen"
	allowedLevels     = "trace|debug|info|warn|error|fatal|panic"
	defaultModuleKey  = "*"
)

// Config is the resolved logging configuration.
type Config struct {
	// LevelSpec is the normalized log_level value: a level name or a module
	// filter list "module:level,...,*:level".
	LevelSpec string
	// Level applies to entries without a module, or with a module not listed in Modules.
	// When the filter list has no "*" entry it is the lowest level (unfiltered).
	Level zapcore.Level
	// Modules holds per-module levels from the filter list (nil when none).
	Modules map[string]zapcore.Level
	// Format is either FormatPlain or FormatJSON.
	Format string
	// Color enables ANSI colors. Always false when Format is FormatJSON.
	Color bool
	// Timestamp is one of the Timestamp* constants ("" is normalized to TimestampOff).
	Timestamp string
	// Trace enables stack traces on error entries.
	Trace bool
}

// EnvVar is a name/value pair used to propagate the configuration to other processes.
type EnvVar struct {
	Name  string
	Value string
}

// DefaultConfig returns the configuration used when no option is set.
func DefaultConfig() Config {
	cfg, err := ParseConfig(DefaultLevel, DefaultFormat, DefaultColor, DefaultTimestamp, DefaultTrace)
	if err != nil {
		panic(err)
	}

	return cfg
}

// ParseConfig validates and normalizes raw option values.
func ParseConfig(level, format string, color bool, timestamp string, trace bool) (Config, error) {
	cfg := Config{
		Color: color,
		Trace: trace,
	}

	switch f := strings.ToLower(strings.TrimSpace(format)); f {
	case FormatPlain, "":
		cfg.Format = FormatPlain
	case FormatJSON:
		cfg.Format = FormatJSON
		cfg.Color = false
	default:
		return Config{}, fmt.Errorf("invalid %s %q: allowed values %s", FlagLogFormat, format, allowedFormats) // nolint: err113
	}

	switch ts := strings.ToLower(strings.TrimSpace(timestamp)); ts {
	case TimestampOff, "":
		cfg.Timestamp = TimestampOff
	case TimestampRFC3339, TimestampRFC3339Nano, TimestampKitchen:
		cfg.Timestamp = ts
	default:
		return Config{}, fmt.Errorf("invalid %s %q: allowed values %s", FlagLogTimestamp, timestamp, allowedTimestamps) // nolint: err113
	}

	spec := strings.ToLower(strings.TrimSpace(level))
	if spec == "" {
		spec = DefaultLevel
	}

	lvl, modules, err := parseLevelSpec(spec)
	if err != nil {
		return Config{}, err
	}

	cfg.LevelSpec = spec
	cfg.Level = lvl
	cfg.Modules = modules

	return cfg, nil
}

// ConfigFromViper reads the logging options from v. Options not set in v take their defaults.
func ConfigFromViper(v *viper.Viper) (Config, error) {
	level := DefaultLevel
	format := DefaultFormat
	color := DefaultColor
	timestamp := DefaultTimestamp
	trace := DefaultTrace

	if v.IsSet(FlagLogLevel) {
		level = v.GetString(FlagLogLevel)
	}
	if v.IsSet(FlagLogFormat) {
		format = v.GetString(FlagLogFormat)
	}
	if v.IsSet(FlagLogColor) {
		color = v.GetBool(FlagLogColor)
	}
	if v.IsSet(FlagLogTimestamp) {
		timestamp = v.GetString(FlagLogTimestamp)
	}
	if v.IsSet(FlagTrace) {
		trace = v.GetBool(FlagTrace)
	}

	return ParseConfig(level, format, color, timestamp, trace)
}

// Env returns the configuration as environment variables with the given prefix,
// e.g. prefix "ATTESTATION_" yields ATTESTATION_LOG_LEVEL ... ATTESTATION_TRACE.
func (c Config) Env(prefix string) []EnvVar {
	return []EnvVar{
		{Name: prefix + "LOG_LEVEL", Value: c.LevelSpec},
		{Name: prefix + "LOG_FORMAT", Value: c.Format},
		{Name: prefix + "LOG_COLOR", Value: strconv.FormatBool(c.Color)},
		{Name: prefix + "LOG_TIMESTAMP", Value: c.Timestamp},
		{Name: prefix + "TRACE", Value: strconv.FormatBool(c.Trace)},
	}
}

// moduleLevel returns the level applied to entries of the given module.
func (c Config) moduleLevel(module string) zapcore.Level {
	if lvl, ok := c.Modules[module]; ok && module != "" {
		return lvl
	}

	return c.Level
}

// minLevel returns the lowest level any entry may be logged at.
func (c Config) minLevel() zapcore.Level {
	lvl := c.Level
	for _, l := range c.Modules {
		if l < lvl {
			lvl = l
		}
	}

	return lvl
}

func parseLevel(val string) (zapcore.Level, error) {
	switch val {
	case "trace", "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	case "fatal":
		return zapcore.FatalLevel, nil
	case "panic":
		return zapcore.PanicLevel, nil
	}

	return 0, fmt.Errorf("invalid %s %q: allowed values %s or module filter \"module:level,...,*:level\"", FlagLogLevel, val, allowedLevels) // nolint: err113
}

// parseLevelSpec parses a level name or a cosmos-style module filter list.
// A plain level is equivalent to "*:level".
func parseLevelSpec(spec string) (zapcore.Level, map[string]zapcore.Level, error) {
	if !strings.Contains(spec, ":") {
		lvl, err := parseLevel(spec)
		return lvl, nil, err
	}

	var modules map[string]zapcore.Level
	def := zapcore.DebugLevel
	seen := make(map[string]struct{})

	for _, item := range strings.Split(spec, ",") {
		parts := strings.Split(item, ":")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return 0, nil, fmt.Errorf("invalid %s %q: expected \"module:level\" pairs, got %q", FlagLogLevel, spec, item) // nolint: err113
		}

		module := strings.TrimSpace(parts[0])
		if _, dup := seen[module]; dup {
			return 0, nil, fmt.Errorf("invalid %s %q: duplicate module %q", FlagLogLevel, spec, module) // nolint: err113
		}
		seen[module] = struct{}{}

		lvl, err := parseLevel(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, nil, err
		}

		if module == defaultModuleKey {
			def = lvl
			continue
		}

		if modules == nil {
			modules = make(map[string]zapcore.Level)
		}
		modules[module] = lvl
	}

	return def, modules, nil
}
