package manifest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/logging/logtest"
)

func TestLogWatchdogDoneUsesDeploymentIDKey(t *testing.T) {
	rec := logtest.New()
	s := &service{session: session.New(rec, nil, nil, 0)}
	did := testutil.DeploymentID(t)

	s.logWatchdogDone(did)

	entries := rec.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, "watchdog done", entries[0].Msg)
	require.Equal(t, did, entries[0].KV["did"])
	require.NotContains(t, entries[0].KV, "lid")
}
