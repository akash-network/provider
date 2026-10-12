package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/troian/pubsub"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/logging/logtest"
)

// stalledRegistry returns a server that accepts requests and never answers
// until the test ends, and a channel signalled on every request.
func stalledRegistry(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()

	release := make(chan struct{})
	received := make(chan struct{}, 16)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))

	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	return srv, received
}

func registryLoaderContext(t *testing.T, url string) (context.Context, context.CancelFunc, *logtest.Recorder) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(FlagProviderConfigsURL, url)
	viper.Set(FlagPciDbURL, url+"/pci.ids")
	viper.Set(FlagRegistryQueryPeriod, time.Hour)

	rec := logtest.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, pubsub.New(ctx, 1000))
	ctx = ctxlog.WithLogger(ctx, rec)

	return ctx, cancel, rec
}

func TestRegistryLoaderStopsOnCancelWhileRequestStalled(t *testing.T) {
	srv, received := stalledRegistry(t)
	ctx, cancel, _ := registryLoaderContext(t, srv.URL)

	done := make(chan error, 1)
	go func() { done <- registryLoader(ctx) }()

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("registry was never queried")
	}

	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("registryLoader did not return after its context was cancelled")
	}
}

func TestRegistryLoaderTimesOutStalledRequest(t *testing.T) {
	prev := registryQueryTimeout
	registryQueryTimeout = 100 * time.Millisecond
	t.Cleanup(func() { registryQueryTimeout = prev })

	srv, _ := stalledRegistry(t)
	ctx, cancel, rec := registryLoaderContext(t, srv.URL)

	done := make(chan error, 1)
	go func() { done <- registryLoader(ctx) }()

	require.Eventually(t, func() bool {
		for _, e := range rec.Entries() {
			if e.Msg == "couldn't query inventory registry" && e.KV["err"] != nil {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "stalled registry request did not time out")

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
