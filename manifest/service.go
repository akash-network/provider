package manifest

import (
	"context"
	"errors"
	"time"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	provider "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	tpubsub "github.com/troian/pubsub"

	"github.com/boz/go-lifecycle"

	manifest "github.com/akash-network/akash-api/go/manifest/v2beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/pubsub"
	dquery "github.com/akash-network/node/x/deployment/query"

	clustertypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/event"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

// ErrNotRunning is the error when service is not running
var ErrNotRunning = errors.New("not running")

var (
	manifestManagerGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name:        "provider_manifest_manager",
		Help:        "",
		ConstLabels: nil,
	})

	manifestWatchdogGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name:        "provider_order_watchdog",
		Help:        "",
		ConstLabels: nil,
	})
)

// StatusClient is the interface which includes status of service
//
//go:generate mockery --name StatusClient
type StatusClient interface {
	Status(context.Context) (*apclient.ManifestStatus, error)
	StatusV1(context.Context) (*provider.ManifestStatus, error)
}

// Client is the interface that wraps HandleManifest method
//
//go:generate mockery --name Client
type Client interface {
	Submit(context.Context, dtypes.DeploymentID, manifest.Manifest) error
	IsActive(context.Context, dtypes.DeploymentID) (bool, error)
}

// Service is the interface that includes StatusClient and Handler interfaces. It also wraps Done method
type Service interface {
	StatusClient
	Client
	Done() <-chan struct{}
}

// NewService creates and returns new Service instance
// Manage incoming leases and manifests and pair the two together to construct and emit a ManifestReceived event.
func NewService(ctx context.Context, session session.Session, bus pubsub.Bus, hostnameService clustertypes.HostnameServiceClient, cfg ServiceConfig) (Service, error) {
	session = session.ForModule("provider-manifest")

	sub, err := bus.Subscribe()
	if err != nil {
		return nil, err
	}

	s := &service{
		ctx:             ctx,
		session:         session,
		bus:             bus,
		sub:             sub,
		statusch:        make(chan chan<- *apclient.ManifestStatus),
		mreqch:          make(chan manifestRequest),
		activeCheckCh:   make(chan isActiveCheck),
		managers:        make(map[string]*manager),
		managerch:       make(chan *manager),
		lc:              lifecycle.New(),
		hostnameService: hostnameService,
		config:          cfg,

		watchdogch: make(chan dtypes.DeploymentID),
		watchdogs:  make(map[dtypes.DeploymentID]*watchdog),
	}

	go s.lc.WatchContext(ctx)
	go s.run()

	return s, nil
}

type service struct {
	ctx     context.Context
	config  ServiceConfig
	session session.Session
	bus     pubsub.Bus
	sub     pubsub.Subscriber
	lc      lifecycle.Lifecycle

	statusch      chan chan<- *apclient.ManifestStatus
	mreqch        chan manifestRequest
	activeCheckCh chan isActiveCheck

	managers  map[string]*manager
	managerch chan *manager

	hostnameService clustertypes.HostnameServiceClient

	watchdogs  map[dtypes.DeploymentID]*watchdog
	watchdogch chan dtypes.DeploymentID
}

type manifestRequest struct {
	value *submitRequest
	ch    chan<- error
	ctx   context.Context
}

func (s *service) updateGauges() {
	manifestManagerGauge.Set(float64(len(s.managers)))
	manifestWatchdogGauge.Set(float64(len(s.managers)))
}

type isActiveCheck struct {
	ch         chan<- bool
	Deployment dtypes.DeploymentID
}

func (s *service) IsActive(ctx context.Context, dID dtypes.DeploymentID) (bool, error) {
	ch := make(chan bool, 1)
	req := isActiveCheck{
		Deployment: dID,
		ch:         ch,
	}

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case s.activeCheckCh <- req:
	case <-s.lc.ShuttingDown():
		return false, ErrNotRunning
	case <-s.lc.Done():
		return false, ErrNotRunning
	}

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-s.lc.Done():
		return false, ErrNotRunning
	case result := <-ch:
		return result, nil
	}
}

// Submit incoming manifest request.
func (s *service) Submit(ctx context.Context, did dtypes.DeploymentID, mani manifest.Manifest) error {
	// This needs to be buffered because the goroutine writing to this may get the result
	// after the context has returned an error
	ch := make(chan error, 1)
	req := manifestRequest{
		value: &submitRequest{
			Deployment: did,
			Manifest:   mani,
		},
		ch:  ch,
		ctx: ctx,
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.mreqch <- req:
	case <-s.lc.ShuttingDown():
		return ErrNotRunning
	case <-s.lc.Done():
		return ErrNotRunning
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.lc.Done():
		return ErrNotRunning
	case result := <-ch:
		return result
	}
}

func (s *service) Done() <-chan struct{} {
	return s.lc.Done()
}

func (s *service) Status(ctx context.Context) (*apclient.ManifestStatus, error) {
	ch := make(chan *apclient.ManifestStatus, 1)

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
		return result, nil
	}
}

func (s *service) StatusV1(ctx context.Context) (*provider.ManifestStatus, error) {
	res, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}

	return &provider.ManifestStatus{Deployments: res.Deployments}, nil
}

func (s *service) run() {
	defer s.lc.ShutdownCompleted()
	defer s.sub.Close()

	bus := fromctx.MustPubSubFromCtx(s.ctx)

	s.updateGauges()

	signalch := make(chan struct{}, 1)
	trySignal := func() {
		select {
		case signalch <- struct{}{}:
		case <-s.lc.ShutdownRequest():
		default:
		}
	}

loop:
	for {
		select {
		case err := <-s.lc.ShutdownRequest():
			s.lc.ShutdownInitiated(err)
			break loop
		case ev := <-s.sub.Events():
			switch ev := ev.(type) {
			case event.LeaseWon:
				if ev.LeaseID.GetProvider() != s.session.Provider().Address().String() {
					continue
				}
				s.session.Log().Info("lease won", "lease", ev.LeaseID)
				s.handleLease(ev, true)
			case dtypes.EventDeploymentUpdated:
				s.session.Log().Info("update received", "deployment", ev.ID, "version", ev.Version)

				key := dquery.DeploymentPath(ev.ID)
				if manager := s.managers[key]; manager != nil {
					s.session.Log().Info("deployment updated", "deployment", ev.ID, "version", ev.Version)
					manager.handleUpdate(ev.Version)
				}
			case dtypes.EventDeploymentClosed:
				key := dquery.DeploymentPath(ev.ID)
				if manager := s.managers[key]; manager != nil {
					s.session.Log().Info("deployment closed", "deployment", ev.ID)
					manager.stop()
				}
			case mtypes.EventLeaseClosed:
				if ev.ID.GetProvider() != s.session.Provider().Address().String() {
					continue
				}

				key := dquery.DeploymentPath(ev.ID.DeploymentID())
				if manager := s.managers[key]; manager != nil {
					s.session.Log().Info("lease closed", "lease", ev.ID)
					manager.removeLease(ev.ID)
				}
			}
		case check := <-s.activeCheckCh:
			_, ok := s.managers[dquery.DeploymentPath(check.Deployment)]
			check.ch <- ok
		case req := <-s.mreqch:
			// Cancel the watchdog (if it exists), since a manifest has been received
			s.maybeRemoveWatchdog(req.value.Deployment)

			manager := s.ensureManager(req.value.Deployment)
			// The manager is responsible for putting a result in req.ch
			manager.handleManifest(req)
			trySignal()
		case ch := <-s.statusch:
			ch <- &apclient.ManifestStatus{
				Deployments: uint32(len(s.managers)), // nolint: gosec
			}
		case <-signalch:
			bus.Pub(provider.ManifestStatus{Deployments: uint32(len(s.managers))}, []string{ptypes.PubSubTopicManifestStatus}, tpubsub.WithRetain()) // nolint: gosec
		case manager := <-s.managerch:
			s.session.Log().Info("manager done", "deployment", manager.daddr)

			delete(s.managers, dquery.DeploymentPath(manager.daddr))

			// Cancel the watchdog (if it exists) since the manager has stopped as well
			s.maybeRemoveWatchdog(manager.daddr)

			trySignal()
		case leaseID := <-s.watchdogch:
			s.session.Log().Info("watchdog done", "lease", leaseID)
			delete(s.watchdogs, leaseID)
		}
		s.updateGauges()
	}

	for len(s.managers) > 0 {
		manager := <-s.managerch
		delete(s.managers, dquery.DeploymentPath(manager.daddr))
		s.updateGauges()
	}

	s.session.Log().Debug("draining watchdogs", "qty", len(s.watchdogs))
	for _, watchdog := range s.watchdogs {
		if watchdog != nil {
			leaseID := <-s.watchdogch
			s.session.Log().Info("watchdog done", "lease", leaseID)
		}
	}

	s.session.Log().Info("shutdown complete")
}

func (s *service) maybeRemoveWatchdog(deploymentID dtypes.DeploymentID) {
	if watchdog := s.watchdogs[deploymentID]; watchdog != nil {
		watchdog.stop()
	}
}

func (s *service) handleLease(ev event.LeaseWon, isNew bool) {
	// Only run this if configured to do so
	if isNew && s.config.ManifestTimeout > time.Duration(0) {
		// Create watchdog if it does not exist AND a manifest has not been received yet
		if watchdog := s.watchdogs[ev.LeaseID.DeploymentID()]; watchdog == nil {
			watchdog = newWatchdog(s.session, s.lc.ShuttingDown(), s.watchdogch, ev.LeaseID, s.config.ManifestTimeout)
			s.watchdogs[ev.LeaseID.DeploymentID()] = watchdog
		}
	}

	manager := s.ensureManager(ev.LeaseID.DeploymentID())

	manager.handleLease(ev)
}

func (s *service) ensureManager(did dtypes.DeploymentID) (manager *manager) {
	manager = s.managers[dquery.DeploymentPath(did)]
	if manager == nil {
		manager = newManager(s, did)
		s.managers[dquery.DeploymentPath(did)] = manager
	}
	return manager
}
