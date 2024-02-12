package ip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/tendermint/tendermint/libs/log"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"

	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	clusterutil "github.com/akash-network/provider/cluster/util"
	ipoptypes "github.com/akash-network/provider/operator/ip/types"
)

const (
	ipOperatorHealthPath = "health"
)

var (
	errNotAlive         = errors.New("ip operator is not yet alive")
	errIPOperatorRemote = errors.New("ip operator remote error")
)

/* A client to talk to the Akash implementation of the IP Operator via HTTP */
type client struct {
	sda    clusterutil.ServiceDiscoveryAgent
	client clusterutil.ServiceClient
	log    log.Logger
	l      sync.Locker
}

var (
	_ cip.Client = (*client)(nil)
)

func NewClient(ctx context.Context, logger log.Logger, endpoint *net.SRV) (cip.Client, error) {
	sda, err := clusterutil.NewServiceDiscoveryAgent(ctx, logger, "rest", "operator-ip", "akash-services", endpoint)
	if err != nil {
		return nil, err
	}

	return &client{
		sda: sda,
		log: logger.With("operator", "ip"),
		l:   &sync.Mutex{},
	}, nil
}

func (ipoc *client) String() string {
	return fmt.Sprintf("<%T %p>", ipoc, ipoc)
}

func (ipoc *client) Stop() {
	ipoc.sda.Stop()
}

func (ipoc *client) Check(ctx context.Context) error {
	req, err := ipoc.newRequest(ctx, http.MethodGet, ipOperatorHealthPath, nil)
	if err != nil {
		return err
	}

	response, err := ipoc.client.DoRequest(req)
	if err != nil {
		return err
	}
	ipoc.log.Info("check result", "status", response.StatusCode)

	if response.StatusCode != http.StatusOK {
		return errNotAlive
	}

	return nil
}

func (ipoc *client) newRequest(ctx context.Context, method string, path string, body io.Reader) (*http.Request, error) {
	ipoc.l.Lock()
	defer ipoc.l.Unlock()
	if ipoc.client == nil {
		var err error
		ipoc.client, err = ipoc.sda.GetClient(ctx, false, false)
		if err != nil {
			return nil, err
		}
	}
	return ipoc.client.CreateRequest(ctx, method, path, body)
}

func (ipoc *client) GetIPAddressStatus(ctx context.Context, orderID mtypes.OrderID) ([]cip.LeaseIPStatus, error) {
	path := fmt.Sprintf("ip-lease-status/%s/%d/%d/%d", orderID.GetOwner(), orderID.GetDSeq(), orderID.GetGSeq(), orderID.GetOSeq())
	req, err := ipoc.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	ipoc.log.Debug("asking for IP address status", "method", req.Method, "url", req.URL)
	response, err := ipoc.client.DoRequest(req)
	if err != nil {
		return nil, err
	}
	ipoc.log.Debug("ip address status request result", "status", response.StatusCode)

	if response.StatusCode == http.StatusNoContent {
		return nil, nil // No data for this lease
	}

	if response.StatusCode != http.StatusOK {
		return nil, extractRemoteError(response)
	}

	var result []cip.LeaseIPStatus

	decoder := json.NewDecoder(response.Body)
	err = decoder.Decode(&result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (ipoc *client) GetIPAddressUsage(ctx context.Context) (cip.AddressUsage, error) {
	req, err := ipoc.newRequest(ctx, http.MethodGet, "usage", nil)
	if err != nil {
		return cip.AddressUsage{}, err
	}

	response, err := ipoc.client.DoRequest(req)
	if err != nil {
		return cip.AddressUsage{}, err
	}

	if response.StatusCode != http.StatusOK {
		return cip.AddressUsage{}, extractRemoteError(response)
	}

	decoder := json.NewDecoder(response.Body)
	result := cip.AddressUsage{}
	err = decoder.Decode(&result)
	if err != nil {
		return cip.AddressUsage{}, err
	}

	return result, nil
}

func extractRemoteError(response *http.Response) error {
	contentType := response.Header["Content-Type"]
	if len(contentType) == 0 || !strings.Contains(contentType[0], "json") {
		return fmt.Errorf("%w: http status %d - unspecified error", errIPOperatorRemote, response.StatusCode)
	}

	// Decode response as JSON error
	body := ipoptypes.IPOperatorErrorResponse{}
	decoder := json.NewDecoder(response.Body)
	err := decoder.Decode(&body)
	if err != nil {
		return err
	}

	if 0 == len(body.Error) {
		return io.EOF
	}

	if body.Code > 0 {
		return ipoptypes.LookupError(body.Code)
	}

	return fmt.Errorf("%w: http status %d - %s", errIPOperatorRemote, response.StatusCode, body.Error)
}
