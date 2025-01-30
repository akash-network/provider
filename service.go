package provider

import (
	"context"
	"time"

	"github.com/boz/go-lifecycle"

	"github.com/pkg/errors"
	tpubsub "github.com/troian/pubsub"

	"github.com/cosmos/cosmos-sdk/client"
	sdktypes "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"

	sclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
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
	Validate(context.Context, sdktypes.Address, dtypes.GroupSpec) (ValidateGroupSpecResult, error)
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
	accAddr sdktypes.AccAddress,
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
		cclient:   cclient,
		bidengine: bidengineSvc,
		manifest:  manifestSvc,
		ctx:       ctx,
		cancel:    cancel,
		bc:        bcSvc,
		lc:        lifecycle.New(),
		config:    cfg,
	}

	go svc.lc.WatchContext(ctx)
	go svc.run()
	go svc.statusRun()

	return svc, nil
}

func queryExistingLeases(
	ctx context.Context,
	aqc sclient.QueryClient,
	accAddr sdktypes.AccAddress,
) ([]mtypes.Bid, error) {
	var presp *sdkquery.PageResponse
	var leases []mtypes.QueryLeaseResponse

	bidsMap := make(map[string]mtypes.Bid)

	limit := uint64(10)
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
			},
			Pagination: preq,
		})
		if err != nil {
			if errorCnt > 1 {
				return nil, err
			}

			errorCnt++

			continue
		}
		errorCnt = 0

		for _, resp := range resp.Bids {
			bidsMap[resp.Bid.BidID.DeploymentID().String()] = resp.Bid
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
			},
			Pagination: preq,
		})
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
		if _, exists := bidsMap[did]; !exists {
			delete(bidsMap, did)
		}
	}

	res := make([]mtypes.Bid, 0, len(bidsMap))

	for _, bid := range bidsMap {
		res = append(res, bid)
	}

	return res, nil
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

func (s *service) Validate(ctx context.Context, owner sdktypes.Address, gspec dtypes.GroupSpec) (ValidateGroupSpecResult, error) {
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
