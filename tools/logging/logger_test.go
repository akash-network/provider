package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	errorsmod "cosmossdk.io/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func mustConfig(t *testing.T, level, format string, color bool, ts string, trace bool) Config {
	t.Helper()
	cfg, err := ParseConfig(level, format, color, ts, trace)
	require.NoError(t, err)
	return cfg
}

func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var res []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		require.True(t, json.Valid([]byte(line)), line)
		m := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		res = append(res, m)
	}
	return res
}

func TestPlainColor(t *testing.T) {
	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "plain", true, "rfc3339", false)).Info("hello", "k", "v")
	require.Contains(t, buf.String(), "\x1b[")
	require.Contains(t, buf.String(), "hello")
}

func TestPlainNoColor(t *testing.T) {
	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "plain", false, "rfc3339", false)).Info("hello", "k", "v")
	require.NotContains(t, buf.String(), "\x1b[")
	require.Contains(t, buf.String(), "hello")
}

func TestJSONNeverColored(t *testing.T) {
	buf := &bytes.Buffer{}
	l := New(buf, mustConfig(t, "debug", "json", true, "rfc3339", false))
	l.Debug("d", "k", 1)
	l.Info("i")
	l.Warn("w")
	l.Error("e", "err", errors.New("boom"))
	require.NotContains(t, buf.String(), "\x1b[")

	lines := jsonLines(t, buf)
	require.Len(t, lines, 4)
	require.Equal(t, "debug", lines[0]["level"])
	require.Equal(t, "d", lines[0]["message"])
	require.Equal(t, "boom", lines[3]["err"])
}

func TestTimestampFormats(t *testing.T) {
	cases := map[string]*regexp.Regexp{
		"rfc3339":     regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(Z|[+-]\d\d:\d\d)$`),
		"rfc3339nano": regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d+(Z|[+-]\d\d:\d\d)$`),
		"kitchen":     regexp.MustCompile(`^\d{1,2}:\d\d(AM|PM)$`),
	}

	for ts, re := range cases {
		buf := &bytes.Buffer{}
		New(buf, mustConfig(t, "info", "json", false, ts, false)).Info("m")
		lines := jsonLines(t, buf)
		require.Len(t, lines, 1)
		val, ok := lines[0]["time"].(string)
		require.True(t, ok, ts)
		require.Regexp(t, re, val, ts)
	}

	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "json", false, "off", false)).Info("m")
	lines := jsonLines(t, buf)
	require.Len(t, lines, 1)
	require.NotContains(t, lines[0], "time")

	buf.Reset()
	New(buf, mustConfig(t, "info", "plain", false, "off", false)).Info("m")
	require.NotContains(t, buf.String(), "<nil>")
	require.True(t, strings.HasPrefix(buf.String(), "INFO"), buf.String())
}

func TestSingleModuleKey(t *testing.T) {
	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "json", false, "off", false)).
		With("module", "a", "k", "v").
		With("module", "b").
		Info("m")

	require.Equal(t, 1, strings.Count(buf.String(), `"module"`), buf.String())
	lines := jsonLines(t, buf)
	require.Equal(t, "b", lines[0]["module"])
	require.Equal(t, "v", lines[0]["k"])
}

func TestModuleFilterBeforeEncoding(t *testing.T) {
	buf := &bytes.Buffer{}
	l := New(buf, mustConfig(t, "bidengine-order:debug,*:info", "json", false, "off", false))

	l.With("module", "bidengine-order").Debug("order-debug")
	l.With("module", "x").Debug("x-debug")
	l.With("module", "x").Info("x-info")
	l.Debug("root-debug")

	lines := jsonLines(t, buf)
	require.Len(t, lines, 2, buf.String())
	require.Equal(t, "order-debug", lines[0]["message"])
	require.Equal(t, "x-info", lines[1]["message"])
}

func TestTraceStack(t *testing.T) {
	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "json", false, "off", true)).Error("m", "err", errors.New("boom"))
	lines := jsonLines(t, buf)
	require.Contains(t, lines[0], "stacktrace")

	buf.Reset()
	New(buf, mustConfig(t, "info", "json", false, "off", false)).Error("m", "err", errors.New("boom"))
	lines = jsonLines(t, buf)
	require.NotContains(t, lines[0], "stacktrace")
}

func TestFormatterErrorNotVerbose(t *testing.T) {
	err := errorsmod.Wrap(errors.New("boom"), "ctx")
	_, isFormatter := err.(fmt.Formatter)
	require.True(t, isFormatter, "test precondition: error must implement fmt.Formatter")

	buf := &bytes.Buffer{}
	New(buf, mustConfig(t, "info", "json", false, "off", false)).Error("m", "err", err)
	lines := jsonLines(t, buf)
	require.Equal(t, "ctx: boom", lines[0]["err"])
	require.NotContains(t, lines[0], "errVerbose")
}

type nilErr struct{}

func (*nilErr) Error() string { panic("nil receiver") }

func TestTypedNilError(t *testing.T) {
	var e *nilErr

	buf := &bytes.Buffer{}
	require.NotPanics(t, func() {
		New(buf, mustConfig(t, "info", "json", false, "off", false)).Error("m", "err", e)
	})
	lines := jsonLines(t, buf)
	require.Equal(t, "<nil>", lines[0]["err"])
}

func TestImplIsZap(t *testing.T) {
	_, ok := New(&bytes.Buffer{}, DefaultConfig()).Impl().(*zap.Logger)
	require.True(t, ok)
}

type customKey string

func TestNonStringModuleKeyIsNormalized(t *testing.T) {
	buf := &bytes.Buffer{}
	l := New(buf, mustConfig(t, "b:debug,*:info", "json", false, "off", false)).
		With("module", "a").
		With(customKey("module"), "b")

	l.Debug("m")

	require.Equal(t, 1, strings.Count(buf.String(), `"module"`), buf.String())
	lines := jsonLines(t, buf)
	require.Len(t, lines, 1, "module b is at debug level, entry must be emitted")
	require.Equal(t, "b", lines[0]["module"])
}
