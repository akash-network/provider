package rest

import (
	"context"
	"net"
	"net/http"

	sdk "github.com/cosmos/cosmos-sdk/types"
	gcontext "github.com/gorilla/context"
	"github.com/tendermint/tendermint/libs/log"

	"github.com/akash-network/provider"
	clfromctx "github.com/akash-network/provider/cluster/types/v1beta3/fromctx"
	gwutils "github.com/akash-network/provider/gateway/utils"
	"github.com/akash-network/provider/tools/fromctx"
)

func NewServer(
	ctx context.Context,
	log log.Logger,
	pclient provider.Client,
	cquery gwutils.CertGetter,
	sni string,
	address string,
	pid sdk.Address,
	clusterConfig map[interface{}]interface{},
) (*http.Server, error) {
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

	tlsCfg, err := gwutils.NewServerTLSConfig(ctx, cquery, sni)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Addr:    address,
		Handler: newRouter(log, pid, pclient, clusterConfig, restMiddleware),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
		TLSConfig: tlsCfg,
	}

	return srv, nil
}
