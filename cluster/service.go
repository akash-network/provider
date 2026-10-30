package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/boz/go-lifecycle"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	tpubsub "github.com/troian/pubsub"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/log"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	dv1 "pkg.akt.dev/go/node/deployment/v1"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	apclient "pkg.akt.dev/go/provider/client"
	provider "pkg.akt.dev/go/provider/v1"
	"pkg.akt.dev/go/util/pubsub"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/operator/waiter"
	crd "github.com/akash-network/provider/pkg/apis/akash.network/v2beta2"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

// ErrNotRunning is an error when service is not running
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
	statusch                       chan chan<- *apclient.ClusterStatus
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
type Cluster interface {
	Reserve(mtypes.OrderID, dtypes.ResourceGroup) (ctypes.Reservation, error)
	Unreserve(mtypes.OrderID) error
}

// StatusClient is the interface which includes status of service
type StatusClient interface {
	Status(context.Context) (*apclient.ClusterStatus, error)
	StatusV1(context.Context) (*provider.ClusterStatus, error)
	FindActiveLease(ctx context.Context, owner sdktypes.Address, dseq uint64, gseq uint32) (bool, mtypes.LeaseID, crd.ManifestGroup, error)
}

// Service manage compute cluster for the provider.  Will eventually integrate with kubernetes, etc...
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
	client Client,
	waiter waiter.OperatorWaiter,
	cfg Config,
) (Service, error) {
	log := session.Log().With("module", "provider-cluster")

	lc := lifecycle.New()

	sub, err := bus.Subscribe()
	if err != nil {
		return nil, err
	}

	deployments, err := findDeployments(ctx, log, client)
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
		log.Debug("found existing hostname", "hostname", v.Hostname, "lid", v.ID)
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
		statusch:                       make(chan chan<- *apclient.ClusterStatus),
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

func (s *service) Status(ctx context.Context) (*apclient.ClusterStatus, error) {
	istatus, err := s.inventory.status(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *apclient.ClusterStatus, 1)

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

	// The lease reconciliation stops with the service.
	reconcileCtx, cancelReconcile := context.WithCancel(ctx)
	defer cancelReconcile()
	s.startDeploymentManagers(reconcileCtx, deployments)

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
			s.log.Debug("received shutdown request", "err", err)
			s.lc.ShutdownInitiated(err)
			break loop
		case ev := <-s.sub.Events():
			switch ev := ev.(type) {
			case event.ManifestReceived:
				s.log.Info("manifest received", "lid", ev.LeaseID)

				mgroup := ev.ManifestGroup()
				if mgroup == nil {
					s.log.Error("indeterminate manifest group", "lid", ev.LeaseID, "group_name", ev.Group.GroupSpec.Name)
					break
				}

				reservation, err := s.inventory.lookup(ev.LeaseID.OrderID(), mgroup)
				if err != nil {
					s.log.Error("error looking up manifest", "err", err, "lid", ev.LeaseID, "group_name", mgroup.Name)
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
						s.log.Error("updating deployment", "err", err, "lid", ev.LeaseID, "group_name", mgroup.Name)
					}
					break
				}

				s.managers[key] = newDeploymentManager(s, deployment, true)

				trySignal()
			case *mtypes.EventLeaseClosed:
				_ = s.bus.Publish(event.LeaseRemoveFundsMonitor{LeaseID: ev.ID})
				s.teardownLease(ev.ID)
			case *mtypes.EventLeaseReclaimStarted:
				s.log.Info("lease reclamation started",
					"lid", ev.ID,
					"reason", ev.Reason,
					"deadline", ev.Deadline)
				// Workloads keep running during the reclamation window. The lease's manager
				// arms a timer for the deadline and broadcasts MsgCloseBid once it elapses in
				// block time; the resulting EventLeaseClosed drives teardown here.
				if manager := s.managers[ev.ID]; manager != nil {
					if err := manager.reclaim(ev.Deadline); err != nil {
						s.log.Error("arming reclamation close", "err", err, "lid", ev.ID)
					}
				} else {
					s.log.Warn("reclaim event for lease with no local manager", "lid", ev.ID)
				}
			}
		case ch := <-s.statusch:
			ch <- &apclient.ClusterStatus{
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
		case update := <-inventorych:
			inv, valid := update.(*provider.Inventory)
			if !valid {
				continue
			}

			msg := provider.ClusterStatus{
				Leases:    provider.Leases{Active: uint32(len(s.managers))}, // nolint: gosec
				Inventory: *inv,
			}
			bus.Pub(msg, []string{ptypes.PubSubTopicClusterStatus}, tpubsub.WithRetain())
		case dm := <-s.managerch:
			s.log.Info("manager done", "lid", dm.deployment.LeaseID())

			// unreserve resources
			if err := s.inventory.unreserve(dm.deployment.LeaseID().OrderID()); err != nil {
				s.log.Error("unreserving inventory",
					"err", err,
					"lid", dm.deployment.LeaseID())
			}

			delete(s.managers, dm.deployment.LeaseID())
			trySignal()
		case req := <-s.checkDeploymentExistsRequestCh:
			s.doCheckDeploymentExists(req)
		}
		s.updateDeploymentManagerGauge()
	}

	s.log.Debug("draining deployment managers...", "count", len(s.managers))
	for _, manager := range s.managers {
		if manager != nil {
			manager := <-s.managerch
			s.log.Debug("manager done", "lid", manager.deployment.LeaseID())
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

// startDeploymentManagers resumes the deployments found in the cluster. Leases
// that closed while the provider was down can leave namespaces and manifests
// behind, so the leases in the cluster are then checked against the chain, in
// the background so the provider does not wait for it. ctx stops that check.
func (s *service) startDeploymentManagers(ctx context.Context, deployments []ctypes.IDeployment) {
	managed := make(map[mtypes.LeaseID]bool, len(deployments))
	for _, deployment := range deployments {
		s.managers[deployment.LeaseID()] = newDeploymentManager(s, deployment, false)
		managed[deployment.LeaseID()] = true
		s.updateDeploymentManagerGauge()
	}

	go s.reconcileLeases(ctx, managed)
}

func (s *service) teardownLease(lid mtypes.LeaseID) {
	if manager := s.managers[lid]; manager != nil {
		if err := manager.teardown(); err != nil {
			s.log.Error("tearing down lease deployment", "err", err, "lid", lid)
		}
		return
	}

	// unreserve resources if no manager present yet.
	if lid.Provider == s.session.Provider().Owner {
		s.log.Info("unreserving unmanaged order", "lid", lid)
		err := s.inventory.unreserve(lid.OrderID())
		if err != nil && !errors.Is(errReservationNotFound, err) {
			s.log.Error("unreserve failed", "lid", lid, "err", err)
		}
	}
}

const (
	// leaseReconcilePageLimit is the number of leases read per chain query.
	leaseReconcilePageLimit = 100
	// leaseReconcileTimeout bounds one reconciliation pass.
	leaseReconcileTimeout       = 5 * time.Minute
	leaseReconcileRetryDelay    = 30 * time.Second
	leaseReconcileMaxRetryDelay = 10 * time.Minute
)

var errLeasePaginationStalled = errors.New("lease pagination stalled")

// reconcileLeases tears down what leases that closed while the provider was
// down left in the cluster. It runs in the background, so startup does not
// wait for the chain, and retries until a pass completes or ctx is done.
// managed holds the leases whose deployment managers started with the service.
// opts override the retry schedule.
func (s *service) reconcileLeases(ctx context.Context, managed map[mtypes.LeaseID]bool, opts ...retry.Option) {
	// Retries only stop once a pass completes or ctx is done, so there is no error to report.
	_ = retry.Do(func() error {
		return s.reconcileLeasesOnce(ctx, managed)
	}, append([]retry.Option{
		retry.Context(ctx),
		// A lease left behind can keep its workloads running, so never give up.
		retry.Attempts(0),
		retry.Delay(leaseReconcileRetryDelay),
		retry.MaxDelay(leaseReconcileMaxRetryDelay),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(_ uint, err error) {
			if ctx.Err() == nil {
				s.log.Error("lease reconciliation failed, retrying", "err", err)
			}
		}),
	}, opts...)...)
}

// reconcileLeasesOnce matches the provider's leases in the cluster against the
// chain. A lease that is not active is torn down only once the chain confirms
// it closed: by its deployment manager if it has one, otherwise by removing
// what it holds in the cluster. Leases the chain does not know are kept. The
// pass fails when the cluster or the chain cannot be read, or a lease cannot
// be confirmed or removed.
func (s *service) reconcileLeasesOnce(ctx context.Context, managed map[mtypes.LeaseID]bool) error {
	ctx, cancel := context.WithTimeout(ctx, leaseReconcileTimeout)
	defer cancel()

	lids, err := s.client.LeaseIDs(ctx)
	if err != nil {
		return err
	}

	owner := s.session.Provider().GetOwner()
	own := make([]mtypes.LeaseID, 0, len(lids))
	for _, lid := range lids {
		if lid.Provider == owner {
			own = append(own, lid)
		}
	}
	if len(own) == 0 {
		return nil
	}

	active, err := s.activeLeases(ctx, owner)
	if err != nil {
		return err
	}

	// Hostnames are reserved per deployment group, so count the leases of each
	// group that are in the cluster or active on chain.
	groupLeases := make(map[dv1.GroupID]int, len(active)+len(own))
	for lid := range active {
		groupLeases[lid.GroupID()]++
	}
	for _, lid := range own {
		if _, exists := active[lid]; !exists {
			groupLeases[lid.GroupID()]++
		}
	}

	var errs error
	for _, lid := range own {
		if _, exists := active[lid]; exists {
			continue
		}

		closed, err := s.closedLease(ctx, lid)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		if closed == nil {
			continue
		}

		s.log.Info("tearing down closed lease", "lid", lid)
		if managed[lid] {
			// Its deployment manager tears it down, as for a lease closed at runtime.
			if err := s.bus.Publish(closed); err != nil {
				errs = errors.Join(errs, err)
			}
			continue
		}
		if err := s.removeUnmanagedLease(ctx, lid, groupLeases[lid.GroupID()] == 1); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

// removeUnmanagedLease removes what a closed lease without a deployment manager
// holds in the cluster, as a manager's teardown does. Its namespace and manifest
// go last, so until everything else is gone the next pass finds the lease again.
// Hostname reservations are held per deployment group, so they are released
// only when the lease is the last of its group.
func (s *service) removeUnmanagedLease(ctx context.Context, lid mtypes.LeaseID, lastOfGroup bool) error {
	if err := errors.Join(
		s.client.PurgeDeclaredHostnames(ctx, lid),
		s.client.PurgeDeclaredIPs(ctx, lid),
	); err != nil {
		return err
	}

	var err error
	if lastOfGroup {
		err = s.hostnames.ReleaseHostnames(lid)
	}

	return errors.Join(err, s.client.TeardownLease(ctx, lid))
}

// activeLeases returns the provider's leases the chain reports active. Leases in
// any other state, reclaiming ones included, are each checked by closedLease.
func (s *service) activeLeases(ctx context.Context, provider string) (map[mtypes.LeaseID]struct{}, error) {
	active := make(map[mtypes.LeaseID]struct{})
	var key []byte
	for {
		resp, err := s.session.Client().Query().Market().Leases(ctx, &mvbeta.QueryLeasesRequest{
			Filters:    mtypes.LeaseFilters{Provider: provider, State: mtypes.LeaseActive.String()},
			Pagination: &sdkquery.PageRequest{Key: key, Limit: leaseReconcilePageLimit},
		})
		if err != nil {
			return nil, err
		}
		for _, lease := range resp.Leases {
			active[lease.Lease.ID] = struct{}{}
		}

		next := resp.GetPagination().GetNextKey()
		if len(next) == 0 {
			return active, nil
		}
		// A node that keeps returning the same page would never let the pass end.
		if bytes.Equal(next, key) {
			return nil, errLeasePaginationStalled
		}
		key = next
	}
}

// closedLease returns the closure of a lease the chain reports closed, and nil
// for a lease in any other state or that the chain does not know.
func (s *service) closedLease(ctx context.Context, lid mtypes.LeaseID) (*mtypes.EventLeaseClosed, error) {
	resp, err := s.session.Client().Query().Market().Lease(ctx, &mvbeta.QueryLeaseRequest{ID: lid})
	if errorsmod.IsOf(err, mtypes.ErrLeaseNotFound) || status.Code(err) == codes.NotFound {
		s.log.Info("keeping lease the chain does not know", "lid", lid)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if resp.Lease.ID != lid {
		return nil, fmt.Errorf("lease query returned a different ID: %s", resp.Lease.ID)
	}

	switch resp.Lease.State {
	case mtypes.LeaseClosed, mtypes.LeaseInsufficientFunds:
		return &mtypes.EventLeaseClosed{ID: lid, Reason: resp.Lease.Reason}, nil
	default:
		return nil, nil
	}
}

func findDeployments(
	ctx context.Context,
	log log.Logger,
	client Client,
) ([]ctypes.IDeployment, error) {
	deployments, err := client.Deployments(ctx)
	if err != nil {
		log.Error("fetching deployments", "err", err)
		return nil, err
	}

	return deployments, nil
}
