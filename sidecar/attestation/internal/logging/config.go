// Package logging configures the attestation sidecar's logger from environment
// variables. It mirrors the provider's tools/logging options, values and defaults;
// the provider injects these variables from its own configuration. Changes go into
// both copies: the provider's tools/logging/testdata/conformance.json is run by both
// test suites.
package logging

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.uber.org/zap/zapcore"
)

// Environment variables configuring the logger.
const (
	EnvLevel     = "ATTESTATION_LOG_LEVEL"
	EnvFormat    = "ATTESTATION_LOG_FORMAT"
	EnvColor     = "ATTESTATION_LOG_COLOR"
	EnvTimestamp = "ATTESTATION_LOG_TIMESTAMP"
	EnvTrace     = "ATTESTATION_TRACE"
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
	allowedLevels     = "trace|debug|info|warn|error|fatal|panic|disabled"
	defaultModuleKey  = "*"
)

// levelDisabled is the level of "disabled": above every level, so no entry passes it.
const levelDisabled = zapcore.InvalidLevel

// zerologLevels names zerolog's numeric levels -1 to 5.
var zerologLevels = [...]string{"trace", "debug", "info", "warn", "error", "fatal", "panic"}

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
		return Config{}, fmt.Errorf("invalid %s %q: allowed values %s", EnvFormat, format, allowedFormats) // nolint: err113
	}

	switch ts := strings.ToLower(strings.TrimSpace(timestamp)); ts {
	case TimestampOff, "":
		cfg.Timestamp = TimestampOff
	case TimestampRFC3339, TimestampRFC3339Nano, TimestampKitchen:
		cfg.Timestamp = ts
	default:
		return Config{}, fmt.Errorf("invalid %s %q: allowed values %s", EnvTimestamp, timestamp, allowedTimestamps) // nolint: err113
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

// ConfigFromEnv reads the configuration using getenv (typically os.Getenv).
// Unset or empty variables take their defaults.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	level := DefaultLevel
	format := DefaultFormat
	color := DefaultColor
	timestamp := DefaultTimestamp
	trace := DefaultTrace

	if val := getenv(EnvLevel); val != "" {
		level = val
	}
	if val := getenv(EnvFormat); val != "" {
		format = val
	}
	if val := getenv(EnvTimestamp); val != "" {
		timestamp = val
	}

	var err error
	if val := getenv(EnvColor); val != "" {
		if color, err = strconv.ParseBool(val); err != nil {
			return Config{}, fmt.Errorf("invalid %s %q: allowed values true|false", EnvColor, val) // nolint: err113
		}
	}
	if val := getenv(EnvTrace); val != "" {
		if trace, err = strconv.ParseBool(val); err != nil {
			return Config{}, fmt.Errorf("invalid %s %q: allowed values true|false", EnvTrace, val) // nolint: err113
		}
	}

	return ParseConfig(level, format, color, timestamp, trace)
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
	case "disabled":
		return levelDisabled, nil
	}

	// zerolog's numeric levels, accepted before the logging rework: lower numbers
	// log everything, higher ones nothing
	if n, err := strconv.Atoi(val); err == nil && n >= math.MinInt8 && n <= math.MaxInt8 {
		switch {
		case n < -1:
			return zapcore.DebugLevel, nil
		case n > 5:
			return levelDisabled, nil
		}

		return parseLevel(zerologLevels[n+1])
	}

	return 0, fmt.Errorf("invalid %s %q: allowed values %s or module filter \"module:level,...,*:level\"", EnvLevel, val, allowedLevels) // nolint: err113
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
			return 0, nil, fmt.Errorf("invalid %s %q: expected \"module:level\" pairs, got %q", EnvLevel, spec, item) // nolint: err113
		}

		module := strings.TrimSpace(parts[0])
		if _, dup := seen[module]; dup {
			return 0, nil, fmt.Errorf("invalid %s %q: duplicate module %q", EnvLevel, spec, module) // nolint: err113
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
