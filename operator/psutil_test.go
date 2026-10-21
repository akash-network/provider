package operator

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/tools/logging/logtest"
)

func TestPsutilServeLogsThroughContextLogger(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	rec := logtest.New()
	ctx, cancel := context.WithCancel(ctxlog.WithLogger(context.Background(), rec))
	t.Cleanup(cancel)

	cmd := cmdPsutilServe()
	cmd.SetArgs([]string{"--api-port", "0"})

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	find := func(msg string) (logtest.Entry, bool) {
		for _, e := range rec.Entries() {
			if e.Msg == msg {
				return e, true
			}
		}
		return logtest.Entry{}, false
	}

	require.Eventually(t, func() bool { _, ok := find("listening"); return ok }, 5*time.Second, 10*time.Millisecond)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("psutil serve did not stop after cancel")
	}

	listening, _ := find("listening")
	require.Equal(t, "psutil", listening.KV["module"])
	require.Equal(t, ":0", listening.KV["listen_addr"])

	_, ok := find("shutting down")
	require.True(t, ok, "shutdown not logged")
}
