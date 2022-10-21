package rest

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/tendermint/tendermint/libs/log"

	ctypes "github.com/ovrclk/akash/x/cert/types/v1beta2"

	"github.com/ovrclk/provider-services"
	"github.com/ovrclk/provider-services/cluster/operatorclients"
	gwutils "github.com/ovrclk/provider-services/gateway/utils"
)

func NewServer(
	ctx context.Context,
	log log.Logger,
	pclient provider.Client,
	cquery ctypes.QueryClient,
	ipopclient operatorclients.IPOperatorClient,
	address string,
	pid sdk.Address,
	certs []tls.Certificate,
	clusterConfig map[interface{}]interface{}) (*http.Server, error) {

	// fixme ovrclk/engineering#609
	// nolint: gosec
	srv := &http.Server{
		Addr:    address,
		Handler: newRouter(log, pid, pclient, ipopclient, clusterConfig),
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
