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

// fakeE2ESuite stands in for an e2e suite without a cluster. With keepRunning
// it ends like a suite that failed in SetupSuite or TearDownSuite.
type fakeE2ESuite struct {
	IntegrationTestSuite
	name        string
	keepRunning bool
	ran         *[]string
}

func (s *fakeE2ESuite) SetupSuite()    { s.running = true }
func (s *fakeE2ESuite) TearDownTest()  {}
func (s *fakeE2ESuite) TearDownSuite() { s.running = s.keepRunning }
func (s *fakeE2ESuite) TestRecordRun() { *s.ran = append(*s.ran, s.name) }

func TestRunE2ESuites(t *testing.T) {
	t.Run("runs every suite that finishes", func(t *testing.T) {
		var ran []string
		err := runE2ESuites(t, []e2eTestingSuite{
			&fakeE2ESuite{name: "first", ran: &ran},
			&fakeE2ESuite{name: "second", ran: &ran},
		})
		require.NoError(t, err)
		require.Equal(t, []string{"first", "second"}, ran)
	})

	// the unfinished suite's test network would block the next suite
	t.Run("stops after a suite that did not finish", func(t *testing.T) {
		var ran []string
		err := runE2ESuites(t, []e2eTestingSuite{
			&fakeE2ESuite{name: "first", ran: &ran},
			&fakeE2ESuite{name: "unfinished", keepRunning: true, ran: &ran},
			&fakeE2ESuite{name: "after", ran: &ran},
		})
		require.Error(t, err)
		require.Equal(t, []string{"first", "unfinished"}, ran)
	})
}
