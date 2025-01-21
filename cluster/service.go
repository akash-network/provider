package cluster

import (
	"context"

	"github.com/boz/go-lifecycle"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	tpubsub "github.com/troian/pubsub"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/tendermint/tendermint/libs/log"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	provider "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/node/pubsub"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/operator/waiter"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

// ErrNotRunning is the error when service is not running
var (
	ErrNotRunning      = errors.New("not running")
	ErrInvalidResource = errors.New("invalid resource")
	errNoManifestGroup = errors.New("no manifest group could be found")
)

var (
	deploymentManagerGauge = promauto.NewGauge(prometheus.GaugeOpts{
		// fixme provider_deployment_manager
		Name:        "provider_deployment_manager",
		Help:        "",
		ConstLabels: nil,
	})
)

type service struct {
	session session.Session
	client  Client
	bus     pubsub.Bus
	sub     pubsub.Subscriber

	inventory *inventoryService
	hostnames *hostnameService

	checkDeploymentExistsRequestCh chan checkDeploymentExistsRequest
	statusch                       chan chan<- *ctypes.Status
	statusV1ch                     chan chan<- uint32
	managers                       map[mtypes.LeaseID]*deploymentManager

	managerch chan *deploymentManager

	log log.Logger
	lc  lifecycle.Lifecycle

	waiter waiter.OperatorWaiter

	config Config
}

type checkDeploymentExistsRequest struct {
	owner sdktypes.Address
	dseq  uint64
	gseq  uint32

	responseCh chan<- mtypes.LeaseID
}

// Cluster is the interface that wraps Reserve and Unreserve methods
//
//go:generate mockery --name Cluster
type Cluster interface {
	Reserve(mtypes.OrderID, dtypes.ResourceGroup) (ctypes.Reservation, error)
	Unreserve(mtypes.OrderID) error
}

// StatusClient is the interface which includes status of service
type StatusClient interface {
	Status(context.Context) (*ctypes.Status, error)
	StatusV1(context.Context) (*provider.ClusterStatus, error)
	FindActiveLease(ctx context.Context, owner sdktypes.Address, dseq uint64, gseq uint32) (bool, mtypes.LeaseID, crd.ManifestGroup, error)
}

// Service manage compute cluster for the provider.  Will eventually integrate with kubernetes, etc...
//
//go:generate mockery --name Service
type Service interface {
	StatusClient
	Cluster
	Close() error
	Ready() <-chan struct{}
	Done() <-chan struct{}
	HostnameService() ctypes.HostnameServiceClient
	TransferHostname(ctx context.Context, leaseID mtypes.LeaseID, hostname string, serviceName string, externalPort uint32) error
}

// NewService returns new Service instance
func NewService(
	ctx context.Context,
	session session.Session,
	bus pubsub.Bus,
	aqc aclient.QueryClient,
	accAddr sdktypes.AccAddress,
	client Client,
	waiter waiter.OperatorWaiter,
	cfg Config,
) (Service, error) {
	log := session.Log().With("module", "provider-cluster", "cmp", "service")

	lc := lifecycle.New()

	sub, err := bus.Subscribe()
	if err != nil {
		return nil, err
	}

	deployments, err := findDeployments(ctx, log, client, aqc, accAddr)
	if err != nil {
		sub.Close()
		return nil, err
	}

	inventory, err := newInventoryService(ctx, cfg, log, sub, client, waiter, deployments)
	if err != nil {
		sub.Close()
		return nil, err
	}

	allHostnames, err := client.AllHostnames(ctx)
	if err != nil {
		sub.Close()
		return nil, err
	}

	// Note: one side effect of this code is to add reservations for auto generated hostnames
	// This is not normally done, but also doesn't cause any problems
	activeHostnames := make(map[string]mtypes.LeaseID, len(allHostnames))
	for _, v := range allHostnames {
		activeHostnames[v.Hostname] = v.ID
		log.Debug("found existing hostname", "hostname", v.Hostname, "id", v.ID)
	}
	hostnames, err := newHostnameService(ctx, cfg, activeHostnames)
	if err != nil {
		return nil, err
	}

	s := &service{
		session:                        session,
		client:                         client,
		hostnames:                      hostnames,
		bus:                            bus,
		sub:                            sub,
		inventory:                      inventory,
		statusch:                       make(chan chan<- *ctypes.Status),
		statusV1ch:                     make(chan chan<- uint32),
		managers:                       make(map[mtypes.LeaseID]*deploymentManager),
		managerch:                      make(chan *deploymentManager),
		checkDeploymentExistsRequestCh: make(chan checkDeploymentExistsRequest),
		log:                            log,
		lc:                             lc,
		config:                         cfg,
		waiter:                         waiter,
	}

	go s.lc.WatchContext(ctx)
	go s.run(ctx, deployments)

	return s, nil
}

func (s *service) FindActiveLease(ctx context.Context, owner sdktypes.Address, dseq uint64, gseq uint32) (bool, mtypes.LeaseID, crd.ManifestGroup, error) {
	response := make(chan mtypes.LeaseID, 1)
	req := checkDeploymentExistsRequest{
		responseCh: response,
		dseq:       dseq,
		gseq:       gseq,
		owner:      owner,
	}
	select {
	case s.checkDeploymentExistsRequestCh <- req:
	case <-ctx.Done():
		return false, mtypes.LeaseID{}, crd.ManifestGroup{}, ctx.Err()
	}

	var leaseID mtypes.LeaseID
	var ok bool
	select {
	case leaseID, ok = <-response:
		if !ok {
			return false, mtypes.LeaseID{}, crd.ManifestGroup{}, nil
		}

	case <-ctx.Done():
		return false, mtypes.LeaseID{}, crd.ManifestGroup{}, ctx.Err()
	}

	found, mgroup, err := s.client.GetManifestGroup(ctx, leaseID)
	if err != nil {
		return false, mtypes.LeaseID{}, crd.ManifestGroup{}, err
	}

	if !found {
		return false, mtypes.LeaseID{}, crd.ManifestGroup{}, errNoManifestGroup
	}

	return true, leaseID, mgroup, nil
}

func (s *service) Close() error {
	s.lc.Shutdown(nil)
	return s.lc.Error()
}

func (s *service) Done() <-chan struct{} {
	return s.lc.Done()
}

func (s *service) Ready() <-chan struct{} {
	return s.inventory.ready()
}

func (s *service) Reserve(order mtypes.OrderID, resources dtypes.ResourceGroup) (ctypes.Reservation, error) {
	return s.inventory.reserve(order, resources)
}

func (s *service) Unreserve(order mtypes.OrderID) error {
	return s.inventory.unreserve(order)
}

func (s *service) HostnameService() ctypes.HostnameServiceClient {
	return s.hostnames
}

func (s *service) TransferHostname(ctx context.Context, leaseID mtypes.LeaseID, hostname string, serviceName string, externalPort uint32) error {
	return s.client.DeclareHostname(ctx, leaseID, hostname, serviceName, externalPort)
}

func (s *service) Status(ctx context.Context) (*ctypes.Status, error) {
	istatus, err := s.inventory.status(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *ctypes.Status, 1)

	select {
	case <-s.lc.Done():
		return nil, ErrNotRunning
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.statusch <- ch:
	}

	select {
	case <-s.lc.Done():
		return nil, ErrNotRunning
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		result.Inventory = istatus
		return result, nil
	}
}

func (s *service) StatusV1(ctx context.Context) (*provider.ClusterStatus, error) {
	istatus, err := s.inventory.statusV1(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan uint32, 1)

	select {
	case <-s.lc.Done():
		return nil, ErrNotRunning
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.statusV1ch <- ch:
	}

	select {
	case <-s.lc.Done():
		return nil, ErrNotRunning
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		res := &provider.ClusterStatus{
			Leases: provider.Leases{
				Active: result,
			},
			Inventory: *istatus,
		}

		return res, nil
	}
}

func (s *service) updateDeploymentManagerGauge() {
	deploymentManagerGauge.Set(float64(len(s.managers)))
}

func (s *service) run(ctx context.Context, deployments []ctypes.IDeployment) {
	defer s.lc.ShutdownCompleted()
	defer s.sub.Close()

	s.updateDeploymentManagerGauge()

	// wait for configured operators to be online & responsive before proceeding
	err := s.waiter.WaitForAll(ctx)
	if err != nil {
		s.lc.ShutdownInitiated(err)
		return
	}

	bus := fromctx.MustPubSubFromCtx(ctx)

	inventorych := bus.Sub(ptypes.PubSubTopicInventoryStatus)

	for _, deployment := range deployments {
		s.managers[deployment.LeaseID()] = newDeploymentManager(s, deployment, false)
		s.updateDeploymentManagerGauge()
	}

	signalch := make(chan struct{}, 1)

	trySignal := func() {
		select {
		case signalch <- struct{}{}:
		case <-ctx.Done():
		default:
		}
	}

	trySignal()

loop:
	for {
		select {
		case err := <-s.lc.ShutdownRequest():
			s.lc.ShutdownInitiated(err)
			break loop
		case ev := <-s.sub.Events():
			switch ev := ev.(type) {
			case event.ManifestReceived:
				s.log.Info("manifest received", "lease", ev.LeaseID)

				mgroup := ev.ManifestGroup()
				if mgroup == nil {
					s.log.Error("indeterminate manifest group", "lease", ev.LeaseID, "group-name", ev.Group.GroupSpec.Name)
					break
				}

				reservation, err := s.inventory.lookup(ev.LeaseID.OrderID(), mgroup)
				if err != nil {
					s.log.Error("error looking up manifest", "err", err, "lease", ev.LeaseID, "group-name", mgroup.Name)
					break
				}

				deployment := &ctypes.Deployment{
					Lid:     ev.LeaseID,
					MGroup:  mgroup,
					CParams: reservation.ClusterParams(),
				}

				key := ev.LeaseID
				if manager := s.managers[key]; manager != nil {
					if err := manager.update(deployment); err != nil {
						s.log.Error("updating deployment", "err", err, "lease", ev.LeaseID, "group-name", mgroup.Name)
					}
					break
				}

				s.managers[key] = newDeploymentManager(s, deployment, true)

				trySignal()
			case mtypes.EventLeaseClosed:
				_ = s.bus.Publish(event.LeaseRemoveFundsMonitor{LeaseID: ev.ID})
				s.teardownLease(ev.ID)
			}
		case ch := <-s.statusch:
			ch <- &ctypes.Status{
				Leases: uint32(len(s.managers)), // nolint: gosec
			}
		case ch := <-s.statusV1ch:
			ch <- uint32(len(s.managers)) // nolint: gosec
		case <-signalch:
			istatus, _ := s.inventory.statusV1(ctx)

			if istatus == nil {
				continue
			}

			msg := provider.ClusterStatus{
				Leases:    provider.Leases{Active: uint32(len(s.managers))}, // nolint: gosec
				Inventory: *istatus,
			}
			bus.Pub(msg, []string{ptypes.PubSubTopicClusterStatus}, tpubsub.WithRetain())
		case <-inventorych:
			trySignal()
		case dm := <-s.managerch:
			s.log.Info("manager done", "lease", dm.deployment.LeaseID())

			// unreserve resources
			if err := s.inventory.unreserve(dm.deployment.LeaseID().OrderID()); err != nil {
				s.log.Error("unreserving inventory",
					"err", err,
					"lease", dm.deployment.LeaseID())
			}

			delete(s.managers, dm.deployment.LeaseID())
			trySignal()
		case req := <-s.checkDeploymentExistsRequestCh:
			s.doCheckDeploymentExists(req)
		}
		s.updateDeploymentManagerGauge()
	}
	s.log.Debug("draining deployment managers...", "qty", len(s.managers))
	for _, manager := range s.managers {
		if manager != nil {
			manager := <-s.managerch
			s.log.Debug("manager done", "lease", manager.deployment.LeaseID())
		}
	}

	<-s.inventory.done()
	s.session.Log().Info("shutdown complete")
}

func (s *service) doCheckDeploymentExists(req checkDeploymentExistsRequest) {
	for leaseID := range s.managers {
		// Check for a match
		if leaseID.GSeq == req.gseq && leaseID.DSeq == req.dseq && leaseID.Owner == req.owner.String() {
			req.responseCh <- leaseID
			return
		}
	}

	close(req.responseCh)
}

func (s *service) teardownLease(lid mtypes.LeaseID) {
	if manager := s.managers[lid]; manager != nil {
		if err := manager.teardown(); err != nil {
			s.log.Error("tearing down lease deployment", "err", err, "lease", lid)
		}
		return
	}

	// unreserve resources if no manager present yet.
	if lid.Provider == s.session.Provider().Owner {
		s.log.Info("unreserving unmanaged order", "lease", lid)
		err := s.inventory.unreserve(lid.OrderID())
		if err != nil && !errors.Is(errReservationNotFound, err) {
			s.log.Error("unreserve failed", "lease", lid, "err", err)
		}
	}
}

func findDeployments(
	ctx context.Context,
	log log.Logger,
	client Client,
	aqc aclient.QueryClient,
	accAddr sdktypes.AccAddress,
) ([]ctypes.IDeployment, error) {
	var presp *sdkquery.PageResponse
	var leases []mtypes.QueryLeaseResponse
	bids := make(map[string]mtypes.Bid)

	limit := uint64(100)
	errorCnt := 0

	for {
		preq := &sdkquery.PageRequest{
			Key:   nil,
			Limit: limit,
		}

		if presp != nil {
			preq.Key = presp.NextKey
		}

		resp, err := aqc.Bids(ctx, &mtypes.QueryBidsRequest{
			Filters: mtypes.BidFilters{
				Provider: accAddr.String(),
				State:    mtypes.BidActive.String(),
			}})
		if err != nil {
			if errorCnt > 1 {
				return nil, err
			}

			errorCnt++

			continue
		}
		errorCnt = 0

		for _, resp := range resp.Bids {
			bids[resp.Bid.BidID.DeploymentID().String()] = resp.Bid
		}

		if uint64(len(resp.Bids)) < limit {
			break
		}

		presp = resp.Pagination
	}

	presp = nil
	for {
		preq := &sdkquery.PageRequest{
			Key:   nil,
			Limit: limit,
		}

		if presp != nil {
			preq.Key = presp.NextKey
		}

		resp, err := aqc.Leases(ctx, &mtypes.QueryLeasesRequest{
			Filters: mtypes.LeaseFilters{
				Provider: accAddr.String(),
				State:    mtypes.LeaseActive.String(),
			}})
		if err != nil {
			if errorCnt > 1 {
				return nil, err
			}

			errorCnt++

			continue
		}

		errorCnt = 0

		leases = append(leases, resp.Leases...)
		if uint64(len(resp.Leases)) < limit {
			break
		}

		presp = resp.Pagination
	}

	for _, resp := range leases {
		did := resp.Lease.LeaseID.DeploymentID().String()
		if _, exists := bids[did]; !exists {
			delete(bids, did)
		}
	}

	deployments, err := client.Deployments(ctx)
	if err != nil {
		log.Error("fetching deployments", "err", err)
		return nil, err
	}

	return deployments, nil
}
