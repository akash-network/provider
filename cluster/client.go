package cluster

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	eventsv1 "k8s.io/api/events/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/tools/remotecommand"

	mani "github.com/akash-network/akash-api/go/manifest/v2beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta3"
	"github.com/akash-network/akash-api/go/node/types/unit"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
	"github.com/akash-network/node/sdl"
	mquery "github.com/akash-network/node/x/market/query"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
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

	AllHostnames(context.Context) ([]ctypes.ActiveHostname, error)
	GetManifestGroup(context.Context, mtypes.LeaseID) (bool, crd.ManifestGroup, error)

	ObserveHostnameState(ctx context.Context) (<-chan ctypes.HostnameResourceEvent, error)
	GetHostnameDeploymentConnections(ctx context.Context) ([]ctypes.LeaseIDHostnameConnection, error)

	ObserveIPState(ctx context.Context) (<-chan ctypes.IPResourceEvent, error)
	GetDeclaredIPs(ctx context.Context, leaseID mtypes.LeaseID) ([]crd.ProviderLeasedIPSpec, error)
}

// Client interface lease and deployment methods
//
//go:generate mockery --name Client
type Client interface {
	ReadClient
	Deploy(ctx context.Context, lID mtypes.LeaseID, mgroup *mani.Group) error
	TeardownLease(context.Context, mtypes.LeaseID) error
	Deployments(context.Context) ([]ctypes.Deployment, error)
	Inventory(context.Context) (ctypes.Inventory, error)
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
	ConnectHostnameToDeployment(ctx context.Context, directive ctypes.ConnectHostnameToDeploymentDirective, sslEnabled bool) error
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

type resourcePair struct {
	allocatable sdk.Int
	allocated   sdk.Int
}

type storageClassState struct {
	resourcePair
	isDefault bool
}

func (rp *resourcePair) dup() resourcePair {
	return resourcePair{
		allocatable: rp.allocatable.AddRaw(0),
		allocated:   rp.allocated.AddRaw(0),
	}
}

func (rp *resourcePair) subNLZ(val types.ResourceValue) bool {
	avail := rp.available()

	res := avail.Sub(val.Val)
	if res.IsNegative() {
		return false
	}

	*rp = resourcePair{
		allocatable: rp.allocatable.AddRaw(0),
		allocated:   rp.allocated.Add(val.Val),
	}

	return true
}

func (rp *resourcePair) available() sdk.Int {
	return rp.allocatable.Sub(rp.allocated)
}

type node struct {
	id               string
	cpu              resourcePair
	gpu              resourcePair
	memory           resourcePair
	ephemeralStorage resourcePair
}

type clusterStorage map[string]*storageClassState

func (cs clusterStorage) dup() clusterStorage {
	res := make(clusterStorage)
	for k, v := range cs {
		res[k] = &storageClassState{
			resourcePair: v.resourcePair.dup(),
			isDefault:    v.isDefault,
		}
	}

	return res
}

type inventory struct {
	storage clusterStorage
	nodes   []*node
}

var _ ctypes.Inventory = (*inventory)(nil)

func (inv *inventory) Adjust(reservation ctypes.Reservation) error {
	resources := make([]types.Resources, len(reservation.Resources().GetResources()))
	copy(resources, reservation.Resources().GetResources())

	currInventory := inv.dup()

nodes:
	for nodeName, nd := range currInventory.nodes {
		// with persistent storage go through iff there is capacity available
		// there is no point to go through any other node without available storage
		currResources := resources[:0]

		for _, res := range resources {
			for ; res.Count > 0; res.Count-- {
				var adjusted bool

				cpu := nd.cpu.dup()
				if adjusted = cpu.subNLZ(res.Resources.CPU.Units); !adjusted {
					continue nodes
				}

				gpu := nd.gpu.dup()
				if res.Resources.GPU != nil {
					if adjusted = gpu.subNLZ(res.Resources.GPU.Units); !adjusted {
						continue nodes
					}
				}

				memory := nd.memory.dup()
				if adjusted = memory.subNLZ(res.Resources.Memory.Quantity); !adjusted {
					continue nodes
				}

				ephemeralStorage := nd.ephemeralStorage.dup()
				storageClasses := currInventory.storage.dup()

				for idx, storage := range res.Resources.Storage {
					attr := storage.Attributes.Find(sdl.StorageAttributePersistent)

					if persistent, _ := attr.AsBool(); !persistent {
						if adjusted = ephemeralStorage.subNLZ(storage.Quantity); !adjusted {
							continue nodes
						}
						continue
					}

					attr = storage.Attributes.Find(sdl.StorageAttributeClass)
					class, _ := attr.AsString()

					if class == sdl.StorageClassDefault {
						for name, params := range storageClasses {
							if params.isDefault {
								class = name

								for i := range storage.Attributes {
									if storage.Attributes[i].Key == sdl.StorageAttributeClass {
										res.Resources.Storage[idx].Attributes[i].Value = class
										break
									}
								}
								break
							}
						}
					}

					cstorage, activeStorageClass := storageClasses[class]
					if !activeStorageClass {
						continue nodes
					}

					if adjusted = cstorage.subNLZ(storage.Quantity); !adjusted {
						// cluster storage does not have enough space thus break to error
						break nodes
					}
				}

				// all requirements for current group have been satisfied
				// commit and move on
				currInventory.nodes[nodeName] = &node{
					id:               nd.id,
					cpu:              cpu,
					gpu:              gpu,
					memory:           memory,
					ephemeralStorage: ephemeralStorage,
				}
			}

			if res.Count > 0 {
				currResources = append(currResources, res)
			}
		}

		resources = currResources
	}

	if len(resources) == 0 {
		*inv = *currInventory

		return nil
	}

	return ctypes.ErrInsufficientCapacity
}

func (inv *inventory) Metrics() ctypes.InventoryMetrics {
	cpuTotal := uint64(0)
	gpuTotal := uint64(0)
	memoryTotal := uint64(0)
	storageEphemeralTotal := uint64(0)
	storageTotal := make(map[string]int64)

	cpuAvailable := uint64(0)
	gpuAvailable := uint64(0)
	memoryAvailable := uint64(0)
	storageEphemeralAvailable := uint64(0)
	storageAvailable := make(map[string]int64)

	ret := ctypes.InventoryMetrics{
		Nodes: make([]ctypes.InventoryNode, 0, len(inv.nodes)),
	}

	for _, nd := range inv.nodes {
		invNode := ctypes.InventoryNode{
			Name: nd.id,
			Allocatable: ctypes.InventoryNodeMetric{
				CPU:              nd.cpu.allocatable.Uint64(),
				Memory:           nd.memory.allocatable.Uint64(),
				StorageEphemeral: nd.ephemeralStorage.allocatable.Uint64(),
			},
		}

		cpuTotal += nd.cpu.allocatable.Uint64()
		gpuTotal += nd.gpu.allocatable.Uint64()

		memoryTotal += nd.memory.allocatable.Uint64()
		storageEphemeralTotal += nd.ephemeralStorage.allocatable.Uint64()

		tmp := nd.cpu.allocatable.Sub(nd.cpu.allocated)
		invNode.Available.CPU = tmp.Uint64()
		cpuAvailable += invNode.Available.CPU

		tmp = nd.gpu.allocatable.Sub(nd.gpu.allocated)
		invNode.Available.GPU = tmp.Uint64()
		gpuAvailable += invNode.Available.GPU

		tmp = nd.memory.allocatable.Sub(nd.memory.allocated)
		invNode.Available.Memory = tmp.Uint64()
		memoryAvailable += invNode.Available.Memory

		tmp = nd.ephemeralStorage.allocatable.Sub(nd.ephemeralStorage.allocated)
		invNode.Available.StorageEphemeral = tmp.Uint64()
		storageEphemeralAvailable += invNode.Available.StorageEphemeral

		ret.Nodes = append(ret.Nodes, invNode)
	}

	ret.TotalAllocatable = ctypes.InventoryMetricTotal{
		CPU:              cpuTotal,
		GPU:              gpuTotal,
		Memory:           memoryTotal,
		StorageEphemeral: storageEphemeralTotal,
		Storage:          storageTotal,
	}

	ret.TotalAvailable = ctypes.InventoryMetricTotal{
		CPU:              cpuAvailable,
		GPU:              gpuAvailable,
		Memory:           memoryAvailable,
		StorageEphemeral: storageEphemeralAvailable,
		Storage:          storageAvailable,
	}

	return ret
}

func (inv *inventory) dup() *inventory {
	res := &inventory{
		nodes: make([]*node, 0, len(inv.nodes)),
	}

	for _, nd := range inv.nodes {
		res.nodes = append(res.nodes, &node{
			id:               nd.id,
			cpu:              nd.cpu.dup(),
			gpu:              nd.gpu.dup(),
			memory:           nd.memory.dup(),
			ephemeralStorage: nd.ephemeralStorage.dup(),
		})
	}

	return res
}

const (
	// 5 CPUs, 5Gi memory for null client.
	nullClientCPU     = 5000
	nullClientGPU     = 2
	nullClientMemory  = 32 * unit.Gi
	nullClientStorage = 512 * unit.Gi
)

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

func (c *nullClient) ObserveHostnameState(_ context.Context) (<-chan ctypes.HostnameResourceEvent, error) {
	return nil, errNotImplemented
}
func (c *nullClient) GetDeployments(_ context.Context, _ dtypes.DeploymentID) ([]ctypes.Deployment, error) {
	return nil, errNotImplemented
}

func (c *nullClient) GetHostnameDeploymentConnections(_ context.Context) ([]ctypes.LeaseIDHostnameConnection, error) {
	return nil, errNotImplemented
}

func (c *nullClient) ConnectHostnameToDeployment(_ context.Context, _ ctypes.ConnectHostnameToDeploymentDirective, _ bool) error {
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

func (c *nullClient) Deploy(ctx context.Context, lid mtypes.LeaseID, mgroup *mani.Group) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

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
			Available: int32(svc.Count),
			Total:     int32(svc.Count),
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

func (c *nullClient) Deployments(context.Context) ([]ctypes.Deployment, error) {
	return nil, nil
}

func (c *nullClient) Inventory(context.Context) (ctypes.Inventory, error) {
	inv := &inventory{
		nodes: []*node{
			{
				id: "solo",
				cpu: resourcePair{
					allocatable: sdk.NewInt(nullClientCPU),
					allocated:   sdk.NewInt(nullClientCPU - 100),
				},
				gpu: resourcePair{
					allocatable: sdk.NewInt(nullClientGPU),
					allocated:   sdk.NewInt(1),
				},
				memory: resourcePair{
					allocatable: sdk.NewInt(nullClientMemory),
					allocated:   sdk.NewInt(nullClientMemory - unit.Gi),
				},
				ephemeralStorage: resourcePair{
					allocatable: sdk.NewInt(nullClientStorage),
					allocated:   sdk.NewInt(nullClientStorage - (10 * unit.Gi)),
				},
			},
		},
		storage: map[string]*storageClassState{
			"beta2": {
				resourcePair: resourcePair{
					allocatable: sdk.NewInt(nullClientStorage),
					allocated:   sdk.NewInt(nullClientStorage - (10 * unit.Gi)),
				},
				isDefault: true,
			},
		},
	}

	return inv, nil
}

func (c *nullClient) Exec(context.Context, mtypes.LeaseID, string, uint, []string, io.Reader, io.Writer, io.Writer, bool, remotecommand.TerminalSizeQueue) (ctypes.ExecResult, error) {
	return nil, errNotImplemented
}

func (c *nullClient) GetManifestGroup(context.Context, mtypes.LeaseID) (bool, crd.ManifestGroup, error) {
	return false, crd.ManifestGroup{}, nil
}

func (c *nullClient) AllHostnames(context.Context) ([]ctypes.ActiveHostname, error) {
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

func (c *nullClient) ObserveIPState(_ context.Context) (<-chan ctypes.IPResourceEvent, error) {
	return nil, errNotImplemented
}

func (c *nullClient) CreateIPPassthrough(_ context.Context, _ mtypes.LeaseID, _ ctypes.ClusterIPPassthroughDirective) error {
	return errNotImplemented
}

func (c *nullClient) PurgeIPPassthrough(_ context.Context, _ mtypes.LeaseID, _ ctypes.ClusterIPPassthroughDirective) error {
	return errNotImplemented
}

func (c *nullClient) PurgeDeclaredIP(_ context.Context, _ mtypes.LeaseID, _ string, _ uint32, _ mani.ServiceProtocol) error {
	return errNotImplemented
}

func (c *nullClient) GetDeclaredIPs(_ context.Context, _ mtypes.LeaseID) ([]crd.ProviderLeasedIPSpec, error) {
	return nil, errNotImplemented
}
