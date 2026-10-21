package logtest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecorderMergesContext(t *testing.T) {
	err := errors.New("boom")

	r := New()
	r.With("module", "a", "lid", "x").Error("failed", "err", err)
	r.Info("plain")

	entries := r.Entries()
	require.Len(t, entries, 2)
	require.Equal(t, Entry{Level: "error", Msg: "failed", KV: map[string]any{"module": "a", "lid": "x", "err": err}}, entries[0])
	require.Equal(t, Entry{Level: "info", Msg: "plain", KV: map[string]any{}}, entries[1])
}
