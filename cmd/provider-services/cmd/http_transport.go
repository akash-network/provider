package cmd

import (
	"net"
	"net/http"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
)

// configureHTTPTransportForConnectionPooling configures the Cosmos SDK client context
// to use connection pooling to prevent ephemeral port exhaustion when making many
// concurrent RPC calls to the blockchain node.
func configureHTTPTransportForConnectionPooling(cctx client.Context) client.Context {
	// Create a custom HTTP transport with connection pooling
	transport := &http.Transport{
		// Connection pooling settings
		MaxIdleConns:        100,              // Maximum number of idle connections across all hosts
		MaxIdleConnsPerHost: 10,               // Maximum number of idle connections per host
		MaxConnsPerHost:     50,               // Maximum number of connections per host
		IdleConnTimeout:     90 * time.Second, // How long idle connections are kept alive
		
		// Connection timeouts
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second, // Keep-alive period
		}).DialContext,
		
		// TLS and HTTP/2 settings
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		
		// Enable connection reuse
		DisableKeepAlives: false,
		
		// Force HTTP/1.1 to ensure better connection pooling behavior
		// Some RPC nodes may not handle HTTP/2 connection pooling optimally
		ForceAttemptHTTP2: false,
	}

	// Create HTTP client with the pooled transport
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second, // Overall request timeout
	}

	// If the client context already has an RPC client, replace it with one that uses our pooled transport
	if cctx.Client != nil {
		nodeURI := cctx.NodeURI
		if nodeURI != "" {
			// Create new RPC client with pooled HTTP transport
			rpcClient, err := rpchttp.NewWithClient(nodeURI, "/websocket", httpClient)
			if err == nil {
				cctx = cctx.WithClient(rpcClient)
			}
		}
	}

	return cctx
}

// createPooledRPCClient creates an RPC client with connection pooling
func createPooledRPCClient(nodeURI string) (rpcclient.Client, error) {
	// Create the same pooled transport as above
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     50,
		IdleConnTimeout:     90 * time.Second,
		
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		ForceAttemptHTTP2:     false,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}

	return rpchttp.NewWithClient(nodeURI, "/websocket", httpClient)
}
