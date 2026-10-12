package kube

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/tools/logging/logtest"
)

func TestTeardownLeaseLogsLeaseID(t *testing.T) {
	rec := logtest.New()
	c := clientForTest(t, nil, nil).(*client)
	c.log = rec

	lid := testutil.LeaseID(t)
	require.NoError(t, c.TeardownLease(context.Background(), lid))

	entries := rec.Entries()
	require.NotEmpty(t, entries)

	var sawError bool
	for _, e := range entries {
		require.Equal(t, lid, e.KV["lid"], "entry %q must carry the lease ID", e.Msg)
		sawError = sawError || e.Msg == "teardown lease: unable to delete namespace"
	}
	require.True(t, sawError, "expected the namespace deletion failure to be logged")
}
