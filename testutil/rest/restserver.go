package rest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	gwutils "github.com/akash-network/provider/gateway/utils"
)

func NewServer(ctx context.Context, t testing.TB, handler http.Handler, cquery gwutils.CertGetter, sni string) *httptest.Server {
	t.Helper()

	ts := httptest.NewUnstartedServer(handler)

	ts.Config.BaseContext = func(_ net.Listener) context.Context {
		return ctx
	}

	var err error
	ts.TLS, err = gwutils.NewServerTLSConfig(ctx, cquery, sni)
	if err != nil {
		t.Fatal(err.Error())
	}

	ts.StartTLS()

	return ts
}
