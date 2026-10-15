//go:build e2e

package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// A retried request must carry its body again: the first attempt consumes it.
// The 503 carries a body like nginx's error page, so its connection is not
// reused and the transport cannot rewind the request body on its own.
func TestQueryAppWithRetriesResendsBody(t *testing.T) {
	var mu sync.Mutex
	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)

		mu.Lock()
		bodies = append(bodies, string(data))
		first := len(bodies) == 1
		mu.Unlock()

		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "<html><head><title>503 Service Temporarily Unavailable</title></head></html>")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp := queryAppWithRetries(t, srv.URL+"/SET/value", "test.localhost", 3, queryWithBody([]byte("payload")))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{"payload", "payload"}, bodies)
}
