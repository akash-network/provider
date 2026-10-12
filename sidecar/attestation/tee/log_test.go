package tee

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/akash-network/attestation-sidecar/internal/logging"
)

type entry struct {
	msg string
	kv  map[string]any
}

type recordingLogger struct {
	mu      *sync.Mutex
	entries *[]entry
	ctx     []any
}

func newRecordingLogger() recordingLogger {
	return recordingLogger{mu: &sync.Mutex{}, entries: &[]entry{}}
}

func (r recordingLogger) add(msg string, kv []any) {
	m := map[string]any{}
	all := append(append([]any{}, r.ctx...), kv...)
	for i := 0; i+1 < len(all); i += 2 {
		m[fmt.Sprint(all[i])] = all[i+1]
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	*r.entries = append(*r.entries, entry{msg: msg, kv: m})
}

func (r recordingLogger) find(msg string) (entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range *r.entries {
		if e.msg == msg {
			return e, true
		}
	}
	return entry{}, false
}

func (r recordingLogger) Debug(msg string, kv ...any) { r.add(msg, kv) }
func (r recordingLogger) Info(msg string, kv ...any)  { r.add(msg, kv) }
func (r recordingLogger) Warn(msg string, kv ...any)  { r.add(msg, kv) }
func (r recordingLogger) Error(msg string, kv ...any) { r.add(msg, kv) }
func (r recordingLogger) With(kv ...any) logging.Logger {
	return recordingLogger{mu: r.mu, entries: r.entries, ctx: append(append([]any{}, r.ctx...), kv...)}
}

// GetQuote against a plain temp directory is deterministic and needs no
// privileges or TEE hardware: it writes inblob, logs, then fails on the
// missing outblob.
func quoteFromTempDir(t *testing.T) error {
	t.Helper()
	_, err := (&ConfigfsTSM{BasePath: t.TempDir()}).GetQuote(context.Background(), [64]byte{})
	return err
}

func TestSetLoggerReceivesTEEMessages(t *testing.T) {
	rec := newRecordingLogger()
	SetLogger(rec)
	t.Cleanup(func() { SetLogger(nil) })

	if err := quoteFromTempDir(t); err == nil {
		t.Fatal("expected missing outblob error")
	}

	e, ok := rec.find("configfs-tsm wrote inblob")
	if !ok {
		t.Fatalf("expected configfs-tsm log entry, got %v", *rec.entries)
	}
	if e.kv["bytes"] != 64 {
		t.Fatalf("expected bytes=64, got %v", e.kv)
	}
}

func TestSetLoggerNilFallsBackToNop(t *testing.T) {
	SetLogger(nil)
	_ = quoteFromTempDir(t)
}
