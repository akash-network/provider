package common

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListenAndServeClosedIsNil(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	srv := &http.Server{Addr: addr, ReadHeaderTimeout: time.Second} // nolint: gosec

	errch := make(chan error, 1)
	go func() { errch <- ListenAndServe(srv) }()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 2*time.Second, 10*time.Millisecond)

	require.NoError(t, srv.Close())

	select {
	case err := <-errch:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Close")
	}
}

func TestListenAndServeReturnsListenError(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:-1", ReadHeaderTimeout: time.Second} // nolint: gosec
	require.Error(t, ListenAndServe(srv))
}
