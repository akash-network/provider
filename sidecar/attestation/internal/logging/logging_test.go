package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestEnvNames(t *testing.T) {
	// contract with the provider: tools/logging Config.Env("ATTESTATION_")
	got := []string{EnvLevel, EnvFormat, EnvColor, EnvTimestamp, EnvTrace}
	want := []string{"ATTESTATION_LOG_LEVEL", "ATTESTATION_LOG_FORMAT", "ATTESTATION_LOG_COLOR", "ATTESTATION_LOG_TIMESTAMP", "ATTESTATION_TRACE"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("env name %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg, err := ConfigFromEnv(envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	want := Config{LevelSpec: "info", Level: zapcore.InfoLevel, Format: "plain", Color: true, Timestamp: "rfc3339"}
	if cfg.LevelSpec != want.LevelSpec || cfg.Level != want.Level || cfg.Format != want.Format ||
		cfg.Color != want.Color || cfg.Timestamp != want.Timestamp || cfg.Trace != want.Trace || cfg.Modules != nil {
		t.Fatalf("defaults: got %+v want %+v", cfg, want)
	}
}

func TestConfigFromEnvValues(t *testing.T) {
	cfg, err := ConfigFromEnv(envFrom(map[string]string{
		EnvLevel:     "tee:debug,*:warn",
		EnvFormat:    "plain",
		EnvColor:     "false",
		EnvTimestamp: "kitchen",
		EnvTrace:     "true",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Level != zapcore.WarnLevel || cfg.Modules["tee"] != zapcore.DebugLevel || cfg.Color ||
		cfg.Timestamp != "kitchen" || !cfg.Trace {
		t.Fatalf("unexpected config %+v", cfg)
	}

	cfg, err = ConfigFromEnv(envFrom(map[string]string{EnvTimestamp: "off"}))
	if err != nil || cfg.Timestamp != "off" {
		t.Fatalf("off: %+v %v", cfg, err)
	}
}

func TestConfigFromEnvInvalid(t *testing.T) {
	for _, env := range []map[string]string{
		{EnvFormat: "text"},
		{EnvTimestamp: "iso"},
		{EnvLevel: "loud"},
		{EnvColor: "maybe"},
		{EnvTrace: "maybe"},
	} {
		if _, err := ConfigFromEnv(envFrom(env)); err == nil {
			t.Fatalf("expected error for %v", env)
		}
	}
}

func TestJSONForcesNoColor(t *testing.T) {
	cfg, err := ConfigFromEnv(envFrom(map[string]string{EnvFormat: "json", EnvColor: "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Color {
		t.Fatal("json must force color off")
	}

	buf := &bytes.Buffer{}
	New(buf, cfg).With("module", "tee").Error("failed", "err", errors.New("boom"))

	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("ANSI in json output: %q", buf.String())
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["module"] != "tee" || m["err"] != "boom" || m["level"] != "error" || m["message"] != "failed" {
		t.Fatalf("unexpected entry %v", m)
	}
}

func TestPlainColorAndModuleFilter(t *testing.T) {
	cfg, err := ConfigFromEnv(envFrom(map[string]string{EnvLevel: "tee:debug,*:info", EnvTimestamp: "off"}))
	if err != nil {
		t.Fatal(err)
	}

	buf := &bytes.Buffer{}
	l := New(buf, cfg)
	l.With("module", "tee").Debug("tee-debug")
	l.Debug("root-debug")

	out := buf.String()
	if !strings.Contains(out, "\x1b[") || !strings.Contains(out, "tee-debug") || strings.Contains(out, "root-debug") {
		t.Fatalf("unexpected output %q", out)
	}
	if strings.Contains(out, "<nil>") {
		t.Fatalf("timestamp off must not print <nil>: %q", out)
	}
}

func TestNop(t *testing.T) {
	Nop().With("module", "x").Error("ignored", "err", errors.New("boom"))
}

type customKey string

func TestNonStringModuleKeyIsNormalized(t *testing.T) {
	cfg, err := ConfigFromEnv(envFrom(map[string]string{EnvLevel: "b:debug,*:info", EnvFormat: "json", EnvTimestamp: "off"}))
	if err != nil {
		t.Fatal(err)
	}

	buf := &bytes.Buffer{}
	New(buf, cfg).With("module", "a").With(customKey("module"), "b").Debug("m")

	if n := strings.Count(buf.String(), `"module"`); n != 1 {
		t.Fatalf("expected exactly one module key, got %d: %s", n, buf.String())
	}

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("entry not emitted or invalid: %v (%q)", err, buf.String())
	}
	if m["module"] != "b" {
		t.Fatalf("expected module b, got %v", m["module"])
	}
}
