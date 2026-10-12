package logging

import (
	"errors"
	"io"
	"testing"

	cosmoslog "cosmossdk.io/log"
	"github.com/rs/zerolog"
)

var errBench = errors.New("boom")

func benchLog(b *testing.B, l cosmoslog.Logger) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		l.Info("bid complete", "oid", "akash1abc/1/1/1", "price", "1uakt", "count", 3, "ok", true, "err", errBench)
	}
}

func benchConfig(b *testing.B, level, format string) Config {
	cfg, err := ParseConfig(level, format, false, TimestampRFC3339, false)
	if err != nil {
		b.Fatal(err)
	}
	return cfg
}

func BenchmarkPlain(b *testing.B) {
	benchLog(b, New(io.Discard, benchConfig(b, "info", FormatPlain)).With("module", "bidengine-order"))
}

func BenchmarkJSON(b *testing.B) {
	benchLog(b, New(io.Discard, benchConfig(b, "info", FormatJSON)).With("module", "bidengine-order"))
}

func BenchmarkDroppedDebug(b *testing.B) {
	l := New(io.Discard, benchConfig(b, "bidengine-order:debug,*:info", FormatJSON)).With("module", "x")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		l.Debug("dropped", "oid", "akash1abc/1/1/1", "count", 3)
	}
}

func BenchmarkCosmosZerologPlain(b *testing.B) {
	benchLog(b, cosmoslog.NewLogger(io.Discard,
		cosmoslog.ColorOption(false),
		cosmoslog.LevelOption(zerolog.InfoLevel)).With("module", "bidengine-order"))
}

func BenchmarkCosmosZerologJSON(b *testing.B) {
	benchLog(b, cosmoslog.NewLogger(io.Discard,
		cosmoslog.OutputJSONOption(),
		cosmoslog.LevelOption(zerolog.InfoLevel)).With("module", "bidengine-order"))
}
