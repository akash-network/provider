package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/boz/go-lifecycle"
	tpubsub "github.com/troian/pubsub"

	"github.com/cosmos/cosmos-sdk/client"
	sdktypes "github.com/cosmos/cosmos-sdk/types"

	aclient "pkg.akt.dev/go/node/client/discovery"
	sclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"
	providerv1 "pkg.akt.dev/go/provider/v1"

	"github.com/akash-network/provider/bidengine"
	"github.com/akash-network/provider/cluster"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/manifest"
	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	ptypes "github.com/akash-network/provider/types"

	"pkg.akt.dev/go/util/pubsub"
)

// BidScreeningClient is the interface to screen if provider will bid on given groupspec
type BidScreeningClient interface {
	BidScreening(context.Context, *providerv1.BidScreeningRequest) (*providerv1.BidScreeningResponse, error)
}

// StatusClient is the interface which includes status of service
type StatusClient interface {
	Status(context.Context) (*apclient.ProviderStatus, error)
	StatusV1(ctx context.Context) (*providerv1.Status, error)
}

type GatewayClient interface {
	StatusClient
	BidScreeningClient
}

type Client interface {
	GatewayClient
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
		return nil, fmt.Errorf("%w: %s", err, errmsg)
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

func (s *service) StatusV1(ctx context.Context) (*providerv1.Status, error) {
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
	return &providerv1.Status{
		Cluster:   clusterSt,
		BidEngine: bidengineSt,
		Manifest:  manifestSt,
		PublicHostnames: []string{
			s.config.ClusterPublicHostname,
		},
		Timestamp: time.Now().UTC(),
	}, nil
}

type ctxKeyOwner struct{}

// ContextWithOwner returns a new context with the owner address stored.
func ContextWithOwner(ctx context.Context, owner sdktypes.Address) context.Context {
	return context.WithValue(ctx, ctxKeyOwner{}, owner)
}

func ownerFromCtx(ctx context.Context) sdktypes.Address {
	val, _ := ctx.Value(ctxKeyOwner{}).(sdktypes.Address)
	return val
}

func (s *service) BidScreening(ctx context.Context, req *providerv1.BidScreeningRequest) (*providerv1.BidScreeningResponse, error) {
	gspec := req.GetGroupSpec()
	if gspec == nil {
		return &providerv1.BidScreeningResponse{
			Passed:  false,
			Reasons: []string{"missing group spec"},
		}, nil
	}

	group := &dtypes.Group{GroupSpec: *gspec}

	owner := ownerFromCtx(ctx)
	ownerEmpty := owner == nil || owner.Empty()

	if ownerEmpty && len(req.GetHostnames()) > 0 {
		return &providerv1.BidScreeningResponse{
			Passed:  false,
			Reasons: []string{"missing owner in context; required for hostname availability check"},
		}, nil
	}

	// Step 1: Run bid screening (eligibility checks)
	params := bidengine.ScreenBidParams{
		Provider:        s.session.Provider(),
		Attributes:      s.config.Attributes,
		MaxGroupVolumes: s.config.MaxGroupVolumes,
		AttrService:     s.bidengine.ProviderAttrService(),
		HostnameService: s.cluster.HostnameService(),
		Owner:           owner,
		Hostnames:       req.GetHostnames(),
		Log:             s.session.Log(),
	}

	screenResult, err := bidengine.ScreenBid(ctx, group, params)
	if err != nil {
		return nil, err
	}

	if !screenResult.Passed {
		return &providerv1.BidScreeningResponse{
			Passed:  false,
			Reasons: screenResult.Reasons,
		}, nil
	}

	// Step 2: Dry-run cluster reservation (capacity check without mutation)
	reservation, err := s.cluster.Reserve(mtypes.OrderID{}, gspec, ctypes.WithDryRun())
	if err != nil {
		return &providerv1.BidScreeningResponse{
			Passed:  false,
			Reasons: append(screenResult.Reasons, "cluster capacity: "+err.Error()),
		}, nil
	}

	// Step 3: Calculate price with allocated resources
	var ownerStr string
	if !ownerEmpty {
		ownerStr = owner.String()
	}

	priceReq := bidengine.Request{
		Owner:              ownerStr,
		GSpec:              gspec,
		PricePrecision:     bidengine.DefaultPricePrecision,
		AllocatedResources: reservation.GetAllocatedResources(),
	}

	price, err := s.config.BidPricingStrategy.CalculatePrice(ctx, priceReq)
	if err != nil {
		return nil, err
	}

	return &providerv1.BidScreeningResponse{
		Passed: true,
		Price:  &price,
	}, nil
}

func (s *service) run() {
	defer s.lc.ShutdownCompleted()

	var shutdownErr error

	// Wait for any service to finish
	select {
	case shutdownErr = <-s.lc.ShutdownRequest():
		s.session.Log().Info("received shutdown request", "err", shutdownErr)
	case <-s.cluster.Done():
		shutdownErr = s.cluster.Close()
		if shutdownErr != nil {
			s.session.Log().Error("cluster service terminated with error", "err", shutdownErr)
		}
	case <-s.bidengine.Done():
		shutdownErr = s.bidengine.Close()
		if shutdownErr != nil {
			s.session.Log().Error("bidengine service terminated with error", "err", shutdownErr)
		}
	case <-s.manifest.Done():
		shutdownErr = s.manifest.Close()
		if shutdownErr != nil {
			s.session.Log().Error("manifest service terminated with error", "err", shutdownErr)
		}
	}

	s.session.Log().Info("shutting down services")
	s.lc.ShutdownInitiated(shutdownErr)
	s.cancel()

	// Wait for all services to finish
	<-s.cluster.Done()
	<-s.bidengine.Done()
	<-s.manifest.Done()
	<-s.bc.lc.Done()

	if err := s.bc.Close(); err != nil {
		s.session.Log().Error("balance checker had error", "err", err)
	}

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

	status := providerv1.Status{
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
			case providerv1.ClusterStatus:
				status.Timestamp = time.Now().UTC()
				status.Cluster = &obj
			case providerv1.BidEngineStatus:
				status.Timestamp = time.Now().UTC()
				status.BidEngine = &obj
			case providerv1.ManifestStatus:
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
