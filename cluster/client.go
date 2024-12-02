package cluster

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/pkg/errors"
	eventsv1 "k8s.io/api/events/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/tools/remotecommand"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	mquery "github.com/akash-network/node/x/market/query"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
)

// Errors types returned by the Exec function on the client interface
var (
	ErrExec                        = errors.New("remote command execute error")
	ErrExecNoServiceWithName       = fmt.Errorf("%w: no such service exists with that name", ErrExec)
	ErrExecServiceNotRunning       = fmt.Errorf("%w: service with that name is not running", ErrExec)
	ErrExecCommandExecutionFailed  = fmt.Errorf("%w: command execution failed", ErrExec)
	ErrExecCommandDoesNotExist     = fmt.Errorf("%w: command could not be executed because it does not exist", ErrExec)
	ErrExecDeploymentNotYetRunning = fmt.Errorf("%w: deployment is not yet active", ErrExec)
	ErrExecPodIndexOutOfRange      = fmt.Errorf("%w: pod index out of range", ErrExec)
	ErrUnknownStorageClass         = errors.New("inventory: unknown storage class")
	errNotImplemented              = errors.New("not implemented")
)

var _ Client = (*nullClient)(nil)

//go:generate mockery --name ReadClient
type ReadClient interface {
	LeaseStatus(context.Context, mtypes.LeaseID) (map[string]*ctypes.ServiceStatus, error)
	ForwardedPortStatus(context.Context, mtypes.LeaseID) (map[string][]ctypes.ForwardedPortStatus, error)
	LeaseEvents(context.Context, mtypes.LeaseID, string, bool) (ctypes.EventsWatcher, error)
	LeaseLogs(context.Context, mtypes.LeaseID, string, bool, *int64) ([]*ctypes.ServiceLog, error)
	ServiceStatus(context.Context, mtypes.LeaseID, string) (*ctypes.ServiceStatus, error)

	AllHostnames(context.Context) ([]chostname.ActiveHostname, error)
	GetManifestGroup(context.Context, mtypes.LeaseID) (bool, crd.ManifestGroup, error)

	ObserveHostnameState(ctx context.Context) (<-chan chostname.ResourceEvent, error)
	GetHostnameDeploymentConnections(ctx context.Context) ([]chostname.LeaseIDConnection, error)

	ObserveIPState(ctx context.Context) (<-chan cip.ResourceEvent, error)
	GetDeclaredIPs(ctx context.Context, leaseID mtypes.LeaseID) ([]crd.ProviderLeasedIPSpec, error)
}

// Client interface lease and deployment methods
//
//go:generate mockery --name Client
type Client interface {
	ReadClient
	Deploy(ctx context.Context, deployment ctypes.IDeployment) error
	TeardownLease(context.Context, mtypes.LeaseID) error
	Deployments(context.Context) ([]ctypes.IDeployment, error)
	Exec(ctx context.Context,
		lID mtypes.LeaseID,
		service string,
		podIndex uint,
		cmd []string,
		stdin io.Reader,
		stdout io.Writer,
		stderr io.Writer,
		tty bool,
		tsq remotecommand.TerminalSizeQueue) (ctypes.ExecResult, error)

	// ConnectHostnameToDeployment Connect a given hostname to a deployment
	ConnectHostnameToDeployment(ctx context.Context, directive chostname.ConnectToDeploymentDirective) error
	// RemoveHostnameFromDeployment Remove a given hostname from a deployment
	RemoveHostnameFromDeployment(ctx context.Context, hostname string, leaseID mtypes.LeaseID, allowMissing bool) error

	// DeclareHostname Declare that a given deployment should be connected to a given hostname
	DeclareHostname(ctx context.Context, lID mtypes.LeaseID, host string, serviceName string, externalPort uint32) error
	// PurgeDeclaredHostnames Purge any hostnames associated with a given deployment
	PurgeDeclaredHostnames(ctx context.Context, lID mtypes.LeaseID) error

	PurgeDeclaredHostname(ctx context.Context, lID mtypes.LeaseID, hostname string) error

	// KubeVersion returns the version information of kubernetes running in the cluster
	KubeVersion() (*version.Info, error)

	DeclareIP(ctx context.Context, lID mtypes.LeaseID, serviceName string, port uint32, externalPort uint32, proto mani.ServiceProtocol, sharingKey string, overwrite bool) error
	PurgeDeclaredIP(ctx context.Context, lID mtypes.LeaseID, serviceName string, externalPort uint32, proto mani.ServiceProtocol) error
	PurgeDeclaredIPs(ctx context.Context, lID mtypes.LeaseID) error
}

func ErrorIsOkToSendToClient(err error) bool {
	return errors.Is(err, ErrExec)
}

type nullLease struct {
	ctx    context.Context
	cancel func()
	group  *mani.Group
}

type nullClient struct {
	leases map[string]*nullLease
	mtx    sync.Mutex
}

// NewServiceLog creates and returns a service log with provided details
func NewServiceLog(name string, stream io.ReadCloser) *ctypes.ServiceLog {
	return &ctypes.ServiceLog{
		Name:    name,
		Stream:  stream,
		Scanner: bufio.NewScanner(stream),
	}
}

// NullClient returns nullClient instance
func NullClient() Client {
	return &nullClient{
		leases: make(map[string]*nullLease),
		mtx:    sync.Mutex{},
	}
}

func (c *nullClient) RemoveHostnameFromDeployment(_ context.Context, _ string, _ mtypes.LeaseID, _ bool) error {
	return errNotImplemented
}

func (c *nullClient) ObserveHostnameState(_ context.Context) (<-chan chostname.ResourceEvent, error) {
	return nil, errNotImplemented
}
func (c *nullClient) GetDeployments(_ context.Context, _ dtypes.DeploymentID) ([]ctypes.IDeployment, error) {
	return nil, errNotImplemented
}

func (c *nullClient) GetHostnameDeploymentConnections(_ context.Context) ([]chostname.LeaseIDConnection, error) {
	return nil, errNotImplemented
}

func (c *nullClient) ConnectHostnameToDeployment(_ context.Context, _ chostname.ConnectToDeploymentDirective) error {
	return errNotImplemented
}

func (c *nullClient) DeclareHostname(_ context.Context, _ mtypes.LeaseID, _ string, _ string, _ uint32) error {
	return errNotImplemented
}

func (c *nullClient) PurgeDeclaredHostnames(_ context.Context, _ mtypes.LeaseID) error {
	return errNotImplemented
}

func (c *nullClient) PurgeDeclaredHostname(_ context.Context, _ mtypes.LeaseID, _ string) error {
	return errNotImplemented
}

func (c *nullClient) Deploy(ctx context.Context, deployment ctypes.IDeployment) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	lid := deployment.LeaseID()
	mgroup := deployment.ManifestGroup()

	ctx, cancel := context.WithCancel(ctx)
	c.leases[mquery.LeasePath(lid)] = &nullLease{
		ctx:    ctx,
		cancel: cancel,
		group:  mgroup,
	}

	return nil
}

func (*nullClient) ForwardedPortStatus(context.Context, mtypes.LeaseID) (map[string][]ctypes.ForwardedPortStatus, error) {
	return nil, errNotImplemented
}

func (c *nullClient) LeaseStatus(_ context.Context, lid mtypes.LeaseID) (map[string]*ctypes.ServiceStatus, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	lease, ok := c.leases[mquery.LeasePath(lid)]
	if !ok {
		return nil, nil
	}

	resp := make(map[string]*ctypes.ServiceStatus)
	for _, svc := range lease.group.Services {
		resp[svc.Name] = &ctypes.ServiceStatus{
			Name:      svc.Name,
			Available: int32(svc.Count), // nolint: gosec
			Total:     int32(svc.Count), // nolint: gosec
		}
	}

	return resp, nil
}

func (c *nullClient) LeaseEvents(ctx context.Context, lid mtypes.LeaseID, _ string, follow bool) (ctypes.EventsWatcher, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	lease, ok := c.leases[mquery.LeasePath(lid)]
	if !ok {
		return nil, nil
	}

	if lease.ctx.Err() != nil {
		return nil, nil
	}

	feed := ctypes.NewEventsFeed(ctx)
	go func() {
		defer feed.Shutdown()

		tm := time.NewTicker(7 * time.Second)
		tm.Stop()

		genEvent := func() *eventsv1.Event {
			return &eventsv1.Event{
				EventTime:           v1.NewMicroTime(time.Now()),
				ReportingController: lease.group.GetName(),
			}
		}

		nfollowCh := make(chan *eventsv1.Event, 1)
		count := 0
		if !follow {
			count = rand.Intn(9) // nolint: gosec
			nfollowCh <- genEvent()
		} else {
			tm.Reset(time.Second)
		}

		for {
			select {
			case <-lease.ctx.Done():
				return
			case evt := <-nfollowCh:
				if !feed.SendEvent(evt) || count == 0 {
					return
				}
				count--
				nfollowCh <- genEvent()
				break
			case <-tm.C:
				tm.Stop()
				if !feed.SendEvent(genEvent()) {
					return
				}
				tm.Reset(time.Duration(rand.Intn(9)+1) * time.Second) // nolint: gosec
				break
			}
		}
	}()

	return feed, nil
}

func (c *nullClient) ServiceStatus(_ context.Context, _ mtypes.LeaseID, _ string) (*ctypes.ServiceStatus, error) {
	return nil, nil
}

func (c *nullClient) LeaseLogs(_ context.Context, _ mtypes.LeaseID, _ string, _ bool, _ *int64) ([]*ctypes.ServiceLog, error) {
	return nil, nil
}

func (c *nullClient) TeardownLease(_ context.Context, lid mtypes.LeaseID) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if lease, ok := c.leases[mquery.LeasePath(lid)]; ok {
		delete(c.leases, mquery.LeasePath(lid))
		lease.cancel()
	}

	return nil
}

func (c *nullClient) Deployments(context.Context) ([]ctypes.IDeployment, error) {
	return nil, nil
}

func (c *nullClient) Exec(context.Context, mtypes.LeaseID, string, uint, []string, io.Reader, io.Writer, io.Writer, bool, remotecommand.TerminalSizeQueue) (ctypes.ExecResult, error) {
	return nil, errNotImplemented
}

func (c *nullClient) GetManifestGroup(context.Context, mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	return false, crd.ManifestGroup{}, nil
}

func (c *nullClient) AllHostnames(context.Context) ([]chostname.ActiveHostname, error) {
	return nil, nil
}

func (c *nullClient) KubeVersion() (*version.Info, error) {
	return nil, nil
}

func (c *nullClient) DeclareIP(_ context.Context, _ mtypes.LeaseID, _ string, _ uint32, _ uint32, _ mani.ServiceProtocol, _ string, _ bool) error {
	return errNotImplemented
}

func (c *nullClient) PurgeDeclaredIPs(_ context.Context, _ mtypes.LeaseID) error {
	return errNotImplemented
}

func (c *nullClient) ObserveIPState(_ context.Context) (<-chan cip.ResourceEvent, error) {
	return nil, errNotImplemented
}

func (c *nullClient) CreateIPPassthrough(_ context.Context, _ mtypes.LeaseID, _ cip.ClusterIPPassthroughDirective) error {
	return errNotImplemented
}

func (c *nullClient) PurgeIPPassthrough(_ context.Context, _ mtypes.LeaseID, _ cip.ClusterIPPassthroughDirective) error {
	return errNotImplemented
}

func (c *nullClient) PurgeDeclaredIP(_ context.Context, _ mtypes.LeaseID, _ string, _ uint32, _ mani.ServiceProtocol) error {
	return errNotImplemented
}

func (c *nullClient) GetDeclaredIPs(_ context.Context, _ mtypes.LeaseID) ([]crd.ProviderLeasedIPSpec, error) {
	return nil, errNotImplemented
}
