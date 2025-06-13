package provider

import (
	"context"
	"fmt"
	"time"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/boz/go-lifecycle"

	atypes "github.com/akash-network/akash-api/go/node/audit/v1beta3"
	sclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	provider "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/cosmos/cosmos-sdk/client"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	tpubsub "github.com/troian/pubsub"

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
	Validate(context.Context, sdktypes.Address, dtypes.GroupSpecs) (provider.BidPreCheckResponse, error)
}

// StatusClient is the interface which includes status of service
//
//go:generate mockery --name StatusClient
type StatusClient interface {
	Status(context.Context) (*apclient.ProviderStatus, error)
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

// NewService creates and returns a new Service instance
// Simple wrapper around various services needed for running a provider.
func NewService(ctx context.Context,
	cctx client.Context,
	accAddr sdktypes.AccAddress,
	session session.Session,
	bus pubsub.Bus,
	cclient cluster.Client,
	waiter waiter.OperatorWaiter,
	cfg Config,
) (Service, error) {
	ctx, cancel := context.WithCancel(ctx)

	session = session.ForModule("provider-service")

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		cancel()
		return nil, err
	}

	bcSvc, err := newBalanceChecker(ctx, cl, accAddr, session, bus, cfg.BalanceCheckerCfg)
	if err != nil {
		session.Log().Error("starting balance checker", "err", err)
		cancel()
		return nil, err
	}

	clusterSvc, err := cluster.NewService(ctx, session, bus, cclient, waiter, cfg.Config)
	if err != nil {
		cancel()
		<-bcSvc.lc.Done()
		return nil, err
	}

	bidengineSvc, err := bidengine.NewService(ctx, cl, session, clusterSvc, bus, waiter, bidengine.Config{
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
		<-clusterSvc.Done()
		<-bcSvc.lc.Done()
		return nil, errors.Wrap(err, errmsg)
	}

	providerAttrService, err := bidengine.NewProviderAttrSignatureService(session, bus)
	if err != nil {
		errmsg := "creating provider attribute service"
		session.Log().Error(errmsg, "err", err)
		cancel()
		<-clusterSvc.Done()
		<-bidengineSvc.Done()
		<-bcSvc.lc.Done()
		return nil, errors.Wrap(err, errmsg)
	}

	manifestConfig := manifest.ServiceConfig{
		HTTPServicesRequireAtLeastOneHost: !cfg.DeploymentIngressStaticHosts,
		ManifestTimeout:                   cfg.ManifestTimeout,
		RPCQueryTimeout:                   cfg.RPCQueryTimeout,
		CachedResultMaxAge:                cfg.CachedResultMaxAge,
	}

	manifestSvc, err := manifest.NewService(ctx, session, bus, clusterSvc.HostnameService(), manifestConfig)
	if err != nil {
		session.Log().Error("creating manifest handler", "err", err)
		cancel()
		<-clusterSvc.Done()
		<-bidengineSvc.Done()
		<-bcSvc.lc.Done()
		return nil, err
	}

	svc := &service{
		session:   session,
		bus:       bus,
		cluster:   clusterSvc,
		aqc:       cl,
		cclient:   cclient,
		bidengine: bidengineSvc,
		manifest:  manifestSvc,
		ctx:       ctx,
		cancel:    cancel,
		bc:        bcSvc,
		lc:        lifecycle.New(),
		config:    cfg,
		pass:      providerAttrService,
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
	aqc     sclient.QueryClient

	cluster   cluster.Service
	bidengine bidengine.Service
	manifest  manifest.Service
	bc        *balanceChecker
	pass      bidengine.ProviderAttrSignatureService

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

func (s *service) Status(ctx context.Context) (*apclient.ProviderStatus, error) {
	clusterSt, err := s.cluster.Status(ctx)
	if err != nil {
		return nil, err
	}
	bidengineSt, err := s.bidengine.Status(ctx)
	if err != nil {
		return nil, err
	}
	manifestSt, err := s.manifest.Status(ctx)
	if err != nil {
		return nil, err
	}
	return &apclient.ProviderStatus{
		Cluster:               clusterSt,
		Bidengine:             bidengineSt,
		Manifest:              manifestSt,
		ClusterPublicHostname: s.config.ClusterPublicHostname,
	}, nil
}

func (s *service) StatusV1(ctx context.Context) (*provider.Status, error) {
	clusterSt, err := s.cluster.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	bidengineSt, err := s.bidengine.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	manifestSt, err := s.manifest.StatusV1(ctx)
	if err != nil {
		return nil, err
	}
	return &provider.Status{
		Cluster:   clusterSt,
		BidEngine: bidengineSt,
		Manifest:  manifestSt,
		PublicHostnames: []string{
			s.config.ClusterPublicHostname,
		},
		Timestamp: time.Now().UTC(),
	}, nil
}

func (s *service) Validate(ctx context.Context, owner sdktypes.Address, groups dtypes.GroupSpecs) (provider.BidPreCheckResponse, error) {
	results := provider.BidPreCheckResponse{
		GroupBidPreChecks: make([]*provider.GroupBidPreCheck, len(groups)),
		TotalPrice:        sdktypes.NewDecCoin("uakt", sdktypes.NewInt(0)), // TODO: Suppport different coins
	}

	// Get provider attributes
	attr, err := s.pass.GetAttributes()
	if err != nil {
		return provider.BidPreCheckResponse{}, err
	}

	for i, gspec := range groups {
		// Check provider attributes match
		if !gspec.MatchAttributes(s.config.Attributes) {
			s.session.Log().Info("incompatible provider attributes", "group_spec_attributes", gspec.Requirements.Attributes, "provider_attributes", s.config.Attributes)
			results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
				Name:   gspec.Name,
				CanBid: false,
				Reason: "incompatible provider attributes",
			}
			continue
		}

		// Check resource requirements
		if !gspec.MatchResourcesRequirements(attr) {
			results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
				Name:   gspec.Name,
				CanBid: false,
				Reason: "incompatible attributes for resources requirements",
			}
			continue
		}

		// Check volume limits
		for _, resources := range gspec.GetResourceUnits() {
			if len(resources.Resources.Storage) > s.config.MaxGroupVolumes {
				results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
					Name:   gspec.Name,
					CanBid: false,
					Reason: fmt.Sprintf("group volumes count exceeds (%d > %d)", len(resources.Resources.Storage), s.config.MaxGroupVolumes),
				}
				continue
			}
		}

		// Check signature requirements if any
		if gspec.Requirements.SignedBy.Size() != 0 {
			// Get auditor signatures
			auditors := make([]string, 0)
			auditors = append(auditors, gspec.Requirements.SignedBy.AllOf...)
			auditors = append(auditors, gspec.Requirements.SignedBy.AnyOf...)

			provAttr := []atypes.Provider{
				{
					Owner:      s.session.Provider().Owner,
					Auditor:    "",
					Attributes: s.session.Provider().Attributes,
				},
			}

			gotten := make(map[string]struct{})
			for _, auditor := range auditors {
				_, done := gotten[auditor]
				if done {
					continue
				}
				result, err := s.pass.GetAuditorAttributeSignatures(auditor)
				if err != nil {
					return provider.BidPreCheckResponse{}, err
				}
				provAttr = append(provAttr, result...)
				gotten[auditor] = struct{}{}
			}

			if !gspec.MatchRequirements(provAttr) {
				results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
					Name:   gspec.Name,
					CanBid: false,
					Reason: "attribute signature requirements not met",
				}
				continue
			}
		}

		// Validate basic group spec
		if err := gspec.ValidateBasic(); err != nil {
			results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
				Name:   gspec.Name,
				CanBid: false,
				Reason: fmt.Sprintf("group validation error: %v", err),
			}
			continue
		}

		// Calculate price if all checks pass
		req := bidengine.Request{
			GSpec:          gspec,
			PricePrecision: bidengine.DefaultPricePrecision,
		}

		price, err := s.config.BidPricingStrategy.CalculatePrice(ctx, req)
		if err != nil {
			return provider.BidPreCheckResponse{}, err
		}

		if gspec.Resources[0].Price.IsGTE(price) {
			results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
				Name:        gspec.Name,
				MinBidPrice: price,
				CanBid:      true,
				Reason:      "price calculated successfully",
			}
		} else {
			results.GroupBidPreChecks[i] = &provider.GroupBidPreCheck{
				Name:        gspec.Name,
				MinBidPrice: price,
				CanBid:      false,
				Reason:      "price above minimum",
			}
		}

		results.TotalPrice = results.TotalPrice.Add(price)
	}

	return results, nil
}

func (s *service) run() {
	defer s.lc.ShutdownCompleted()

	// Wait for any service to finish
	select {
	case shutdownErr := <-s.lc.ShutdownRequest():
		s.session.Log().Info("received shutdown request", "err", shutdownErr)
	case <-s.cluster.Done():
	case <-s.bidengine.Done():
	case <-s.manifest.Done():
	}

	s.session.Log().Info("shutting down services")
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
