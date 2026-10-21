// Package logtest provides a cosmossdk.io/log.Logger that records entries for assertions in tests.
package logtest

import (
	"fmt"
	"sync"

	"cosmossdk.io/log"
)

// Entry is one recorded log call. KV holds the call's key/values merged over
// the context added with With.
type Entry struct {
	Level string
	Msg   string
	KV    map[string]any
}

type store struct {
	mu      sync.Mutex
	entries []Entry
}

// Recorder records log entries. Loggers derived with With share the same store.
type Recorder struct {
	store *store
	ctx   []any
}

var _ log.Logger = (*Recorder)(nil)

// New returns an empty Recorder.
func New() *Recorder {
	return &Recorder{store: &store{}}
}

// Entries returns a copy of all recorded entries in call order.
func (r *Recorder) Entries() []Entry {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	res := make([]Entry, len(r.store.entries))
	copy(res, r.store.entries)

	return res
}

func (r *Recorder) Debug(msg string, keyVals ...any) { r.record("debug", msg, keyVals) }
func (r *Recorder) Info(msg string, keyVals ...any)  { r.record("info", msg, keyVals) }
func (r *Recorder) Warn(msg string, keyVals ...any)  { r.record("warn", msg, keyVals) }
func (r *Recorder) Error(msg string, keyVals ...any) { r.record("error", msg, keyVals) }

// With returns a Recorder sharing this recorder's store with additional context.
func (r *Recorder) With(keyVals ...any) log.Logger {
	ctx := make([]any, 0, len(r.ctx)+len(keyVals))
	ctx = append(ctx, r.ctx...)
	ctx = append(ctx, keyVals...)

	return &Recorder{store: r.store, ctx: ctx}
}

// Impl returns the recorder itself.
func (r *Recorder) Impl() any {
	return r
}

func (r *Recorder) record(level, msg string, keyVals []any) {
	kv := make(map[string]any)
	add := func(vals []any) {
		for i := 0; i+1 < len(vals); i += 2 {
			kv[fmt.Sprint(vals[i])] = vals[i+1]
		}
	}

	add(r.ctx)
	add(keyVals)

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.entries = append(r.store.entries, Entry{Level: level, Msg: msg, KV: kv})
}
