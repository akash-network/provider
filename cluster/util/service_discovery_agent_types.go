package util

import (
	"context"
	"io"
	"net/http"

	"github.com/boz/go-lifecycle"
	"github.com/tendermint/tendermint/libs/log"
)

type ServiceDiscoveryAgent interface {
	Stop()
	GetClient(ctx context.Context, isHTTPS, secure bool) (ServiceClient, error)
	DiscoverNow()
}

type ServiceClient interface {
	CreateRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error)
	DoRequest(req *http.Request) (*http.Response, error)
}

type serviceDiscoveryAgent struct {
	ctx         context.Context
	serviceName string
	namespace   string
	portName    string
	lc          lifecycle.Lifecycle

	discoverch chan struct{}

	requests        chan serviceDiscoveryRequest
	pendingRequests []serviceDiscoveryRequest
	result          clientFactory
	log             log.Logger
}

type serviceDiscoveryRequest struct {
	errCh    chan<- error
	resultCh chan<- clientFactory
}

type clientFactory func(isHttps, secure bool) ServiceClient

type httpWrapperServiceClient struct {
	httpClient *http.Client
	url        string
}
