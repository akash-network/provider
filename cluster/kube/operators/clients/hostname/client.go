package hostname

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/tendermint/tendermint/libs/log"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	clusterutil "github.com/akash-network/provider/cluster/util"
)

const (
	hostnameOperatorHealthPath = "health"
)

var (
	errNotAlive = errors.New("hostname operator is not yet alive")
)

type client struct {
	sda    clusterutil.ServiceDiscoveryAgent
	client clusterutil.ServiceClient
	log    log.Logger
	l      sync.Locker
}

var (
	_ chostname.Client = (*client)(nil)
)

func NewClient(ctx context.Context, logger log.Logger, endpoint *net.SRV) (chostname.Client, error) {
	sda, err := clusterutil.NewServiceDiscoveryAgent(ctx, logger, "rest", "operator-hostname", "akash-services", endpoint)
	if err != nil {
		return nil, err
	}

	return &client{
		log: logger.With("operator", "hostname"),
		sda: sda,
		l:   &sync.Mutex{},
	}, nil

}

func (hopc *client) newRequest(ctx context.Context, method string, path string, body io.Reader) (*http.Request, error) {
	hopc.l.Lock()
	defer hopc.l.Unlock()

	if nil == hopc.client {
		var err error
		hopc.client, err = hopc.sda.GetClient(ctx, false, false)
		if err != nil {
			return nil, err
		}
	}

	return hopc.client.CreateRequest(ctx, method, path, body)
}

func (hopc *client) Check(ctx context.Context) error {
	req, err := hopc.newRequest(ctx, http.MethodGet, hostnameOperatorHealthPath, nil)
	if err != nil {
		return err
	}

	response, err := hopc.client.DoRequest(req)
	if err != nil {
		return err
	}
	hopc.log.Info("check result", "status", response.StatusCode)

	if response.StatusCode != http.StatusOK {
		return errNotAlive
	}

	return nil
}

func (hopc *client) String() string {
	return fmt.Sprintf("<%T %p>", hopc, hopc)
}

func (hopc *client) Stop() {
	hopc.sda.Stop()
}
