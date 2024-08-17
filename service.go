package provider

import (
	"context"
	"time"

	"github.com/boz/go-lifecycle"
	"github.com/pkg/errors"
	tpubsub "github.com/troian/pubsub"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	bankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	provider "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/node/pubsub"

	"github.com/akash-network/provider/bidengine"
	aclient "github.com/akash-network/provider/client"
	"github.com/akash-network/provider/cluster"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/manifest"
	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"
)

// ValidateClient is the interface to check if provider will bid on given groupspec
type ValidateClient interface {
	Validate(context.Context, sdk.Address, dtypes.GroupSpec) (ValidateGroupSpecResult, error)
}

// StatusClient is the interface which includes status of service
//
//go:generate mockery --name StatusClient
type StatusClient interface {
	Status(context.Context) (*Status, error)
	StatusV1(ctx context.Context) (*provider.Status, error)
}

//go:generate mockery --name Client
type Client interface {
	StatusClient
	ValidateClient
	Manifest() manifest.Client
	Cluster() cluster.Client
	Hostname() ctypes.HostnameServiceClient
	ClusterService() cluster.Service
}

// Service is the interface that includes StatusClient interface.
// It also wraps ManifestHandler, Close and Done methods.
type Service interface {
	Client

	Close() error
	Done() <-chan struct{}
}

// NewService creates and returns new Service instance
// Simple wrapper around various services needed for running a provider.
func NewService(ctx context.Context,
	cctx client.Context,
	accAddr sdk.AccAddress,
	session session.Session,
	bus pubsub.Bus,
	cclient cluster.Client,
	waiter waiter.OperatorWaiter,
	cfg Config) (Service, error) {
	ctx, cancel := context.WithCancel(ctx)

	session = session.ForModule("provider-service")

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		cancel()
		return nil, err
	}

	bc, err := newBalanceChecker(ctx, bankTypes.NewQueryClient(cctx), cl, accAddr, session, bus, cfg.BalanceCheckerCfg)
	if err != nil {
		session.Log().Error("starting balance checker", "err", err)
		cancel()
		return nil, err
	}

	cluster, err := cluster.NewService(ctx, session, bus, cclient, waiter, cfg.Config)
	if err != nil {
		cancel()
		<-bc.lc.Done()
		return nil, err
	}

	bidengine, err := bidengine.NewService(ctx, session, cluster, bus, waiter, bidengine.Config{
		PricingStrategy: cfg.BidPricingStrategy,
		Deposit:         cfg.BidDeposit,
		BidTimeout:      cfg.BidTimeout,
		Attributes:      cfg.Attributes,
		MaxGroupVolumes: cfg.MaxGroupVolumes,
	})
	if err != nil {
		errmsg := "creating bidengine service"
		session.Log().Error(errmsg, "err", err)
		cancel()
		<-cluster.Done()
		<-bc.lc.Done()
		return nil, errors.Wrap(err, errmsg)
	}

	manifestConfig := manifest.ServiceConfig{
		HTTPServicesRequireAtLeastOneHost: !cfg.DeploymentIngressStaticHosts,
		ManifestTimeout:                   cfg.ManifestTimeout,
		RPCQueryTimeout:                   cfg.RPCQueryTimeout,
		CachedResultMaxAge:                cfg.CachedResultMaxAge,
	}

	manifest, err := manifest.NewService(ctx, session, bus, cluster.HostnameService(), manifestConfig)
	if err != nil {
		session.Log().Error("creating manifest handler", "err", err)
		cancel()
		<-cluster.Done()
		<-bidengine.Done()
		<-bc.lc.Done()
		return nil, err
	}

	svc := &service{
		session:   session,
		bus:       bus,
		cluster:   cluster,
		cclient:   cclient,
		bidengine: bidengine,
		manifest:  manifest,
		ctx:       ctx,
		cancel:    cancel,
		bc:        bc,
		lc:        lifecycle.New(),
		config:    cfg,
	}

	go svc.lc.WatchContext(ctx)
	go svc.run()
	go svc.statusRun()

	return svc, nil
}

type service struct {
	config  Config
	session session.Session
	bus     pubsub.Bus
	cclient cluster.Client

	cluster   cluster.Service
	bidengine bidengine.Service
	manifest  manifest.Service
	bc        *balanceChecker

	ctx    context.Context
	cancel context.CancelFunc
	lc     lifecycle.Lifecycle
}

func (s *service) Hostname() ctypes.HostnameServiceClient {
	return s.cluster.HostnameService()
}

func (s *service) ClusterService() cluster.Service {
	return s.cluster
}

func (s *service) Close() error {
	s.lc.Shutdown(nil)
	return s.lc.Error()
}

func (s *service) Done() <-chan struct{} {
	return s.lc.Done()
}

func (s *service) Manifest() manifest.Client {
	return s.manifest
}

func (s *service) Cluster() cluster.Client {
	return s.cclient
}

func (s *service) Status(ctx context.Context) (*Status, error) {
	cluster, err := s.cluster.Status(ctx)
	if err != nil {
		return nil, err
	}
	bidengine, err := s.bidengine.Status(ctx)
	if err != nil {
		return nil, err
	}
	manifest, err := s.manifest.Status(ctx)
	if err != nil {
		return nil, err
	}
	return &Status{
		Cluster:               cluster,
		Bidengine:             bidengine,
		Manifest:              manifest,
		ClusterPublicHostname: s.config.ClusterPublicHostname,
	}, nil
}

func (s *service) StatusV1(ctx context.Context) (*provider.Status, error) {
	cluster, err := s.cluster.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	bidengine, err := s.bidengine.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	manifest, err := s.manifest.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	return &provider.Status{
		Cluster:   cluster,
		BidEngine: bidengine,
		Manifest:  manifest,
		PublicHostnames: []string{
			s.config.ClusterPublicHostname,
		},
		Timestamp: time.Now().UTC(),
	}, nil
}

func (s *service) Validate(ctx context.Context, owner sdk.Address, gspec dtypes.GroupSpec) (ValidateGroupSpecResult, error) {
	// FUTURE - pass owner here
	req := bidengine.Request{
		Owner: owner.String(),
		GSpec: &gspec,
	}

	// inv, err := s.cclient.Inventory(ctx)
	// if err != nil {
	// 	return ValidateGroupSpecResult{}, err
	// }
	//
	// res := &reservation{
	// 	resources:     nil,
	// 	clusterParams: nil,
	// }
	//
	// if err = inv.Adjust(res, ctypes.WithDryRun()); err != nil {
	// 	return ValidateGroupSpecResult{}, err
	// }

	price, err := s.config.BidPricingStrategy.CalculatePrice(ctx, req)
	if err != nil {
		return ValidateGroupSpecResult{}, err
	}

	return ValidateGroupSpecResult{
		MinBidPrice: price,
	}, nil
}

func (s *service) run() {
	defer s.lc.ShutdownCompleted()

	// Wait for any service to finish
	select {
	case <-s.lc.ShutdownRequest():
	case <-s.cluster.Done():
	case <-s.bidengine.Done():
	case <-s.manifest.Done():
	}

	// Shut down all services
	s.lc.ShutdownInitiated(nil)
	s.cancel()

	// Wait for all services to finish
	<-s.cluster.Done()
	<-s.bidengine.Done()
	<-s.manifest.Done()
	<-s.bc.lc.Done()

	s.session.Log().Info("shutdown complete")
}

func (s *service) statusRun() {
	bus := fromctx.MustPubSubFromCtx(s.ctx)

	events := bus.Sub(
		ptypes.PubSubTopicClusterStatus,
		ptypes.PubSubTopicBidengineStatus,
		ptypes.PubSubTopicManifestStatus,
	)

	defer bus.Unsub(events)

	status := provider.Status{
		PublicHostnames: []string{
			s.config.ClusterPublicHostname,
		},
	}

loop:
	for {
		select {
		case <-s.cluster.Done():
			return
		case evt := <-events:
			switch obj := evt.(type) {
			case provider.ClusterStatus:
				status.Timestamp = time.Now().UTC()
				status.Cluster = &obj
			case provider.BidEngineStatus:
				status.Timestamp = time.Now().UTC()
				status.BidEngine = &obj
			case provider.ManifestStatus:
				status.Timestamp = time.Now().UTC()
				status.Manifest = &obj
			default:
				continue loop
			}

			bus.Pub(status, []string{ptypes.PubSubTopicProviderStatus}, tpubsub.WithRetain())
		}
	}
}

type reservation struct {
	resources         dtypes.ResourceGroup
	adjustedResources dtypes.ResourceUnits
	clusterParams     interface{}
}

var _ ctypes.ReservationGroup = (*reservation)(nil)

func (r *reservation) Resources() dtypes.ResourceGroup {
	return r.resources
}

func (r *reservation) SetAllocatedResources(val dtypes.ResourceUnits) {
	r.adjustedResources = val
}

func (r *reservation) GetAllocatedResources() dtypes.ResourceUnits {
	return r.adjustedResources
}

func (r *reservation) SetClusterParams(val interface{}) {
	r.clusterParams = val
}

func (r *reservation) ClusterParams() interface{} {
	return r.clusterParams
}
