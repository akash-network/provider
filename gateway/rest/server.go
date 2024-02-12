package rest

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	gcontext "github.com/gorilla/context"
	"github.com/tendermint/tendermint/libs/log"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"

	"github.com/akash-network/provider"
	clfromctx "github.com/akash-network/provider/cluster/types/v1beta3/fromctx"
	gwutils "github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/tools/fromctx"
)

func NewServer(
	ctx context.Context,
	log log.Logger,
	pclient provider.Client,
	cquery ctypes.QueryClient,
	address string,
	pid sdk.Address,
	certs []tls.Certificate,
	clusterConfig map[interface{}]interface{}) (*http.Server, error) {

	restMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gcontext.Set(r, fromctx.CtxKeyKubeConfig, fromctx.MustKubeConfigFromCtx(ctx))
			gcontext.Set(r, fromctx.CtxKeyKubeClientSet, fromctx.MustKubeClientFromCtx(ctx))
			gcontext.Set(r, fromctx.CtxKeyAkashClientSet, fromctx.MustAkashClientFromCtx(ctx))

			gcontext.Set(r, clfromctx.CtxKeyClientInventory, clfromctx.ClientInventoryFromContext(ctx))
			gcontext.Set(r, clfromctx.CtxKeyClientHostname, clfromctx.ClientHostnameFromContext(ctx))

			if ip := clfromctx.ClientIPFromContext(ctx); ip != nil {
				gcontext.Set(r, clfromctx.CtxKeyClientIP, ip)
			}

			next.ServeHTTP(w, r)
		})
	}

	// fixme ovrclk/engineering#609
	// nolint: gosec
	srv := &http.Server{
		Addr:    address,
		Handler: newRouter(log, pid, pclient, clusterConfig, restMiddleware),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	var err error

	srv.TLSConfig, err = gwutils.NewServerTLSConfig(context.Background(), certs, cquery)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func NewJwtServer(ctx context.Context,
	cquery ctypes.QueryClient,
	jwtGatewayAddr string,
	providerAddr sdk.Address,
	cert tls.Certificate,
	certSerialNumber string,
	jwtExpiresAfter time.Duration,
) (*http.Server, error) {
	// fixme ovrclk/engineering#609
	// nolint: gosec
	srv := &http.Server{
		Addr:    jwtGatewayAddr,
		Handler: newJwtServerRouter(providerAddr, cert.PrivateKey, jwtExpiresAfter, certSerialNumber),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	var err error
	srv.TLSConfig, err = gwutils.NewServerTLSConfig(ctx, []tls.Certificate{cert}, cquery)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func NewResourceServer(ctx context.Context,
	log log.Logger,
	serverAddr string,
	providerAddr sdk.Address,
	pubkey *ecdsa.PublicKey,
	lokiGwAddr string,
) (*http.Server, error) {
	// fixme ovrclk/engineering#609
	// nolint: gosec
	srv := &http.Server{
		Addr:        serverAddr,
		Handler:     newResourceServerRouter(log, providerAddr, pubkey, lokiGwAddr),
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	return srv, nil
}
