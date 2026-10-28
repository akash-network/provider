package bidengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/boz/go-lifecycle"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	tpubsub "github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	sclient "pkg.akt.dev/go/node/client/v1beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	apclient "pkg.akt.dev/go/provider/client"
	provider "pkg.akt.dev/go/provider/v1"
	"pkg.akt.dev/go/util/pubsub"
	mquery "pkg.akt.dev/node/v2/x/market/query"

	"github.com/akash-network/provider/cluster"
	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/pconfig"
	ptypes "github.com/akash-network/provider/types"
)

var ordersCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "provider_order_handler",
	Help: "The total number of orders created",
}, []string{"action"})

// ErrNotRunning declares new error with message "not running"
var ErrNotRunning = errors.New("not running")

// ordersFetchRetryPeriod is the delay before retrying a failed open orders query
const ordersFetchRetryPeriod = 5 * time.Second

// StatusClient interface predefined with Status method
type StatusClient interface {
	Status(context.Context) (*apclient.BidEngineStatus, error)
	StatusV1(ctx context.Context) (*provider.BidEngineStatus, error)
}

var orderManagerGauge = promauto.NewGauge(prometheus.GaugeOpts{
	Name:        "provider_order_manager",
	Help:        "",
	ConstLabels: nil,
})

// Service handles bidding on orders.
type Service interface {
	StatusClient
	Close() error
	Done() <-chan struct{}
}

// NewService creates new service instance and returns error in case of failure
func NewService(
	pctx context.Context,
	aqc sclient.QueryClient,
	session session.Session,
	cluster cluster.Cluster,
	bus pubsub.Bus,
	waiter waiter.OperatorWaiter,
	cfg Config,
) (Service, error) {
	pcfg, err := fromctx.PersistentConfigFromCtx(pctx)
	if err != nil {
		return nil, err
	}

	session = session.ForModule("bidengine-service")

	sub, err := bus.Subscribe()
	if err != nil {
		return nil, err
	}

	providerAttrService, err := newProviderAttrSignatureService(session, bus)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(pctx)
	group, _ := errgroup.WithContext(ctx)

	s := &service{
		session:  session,
		cluster:  cluster,
		bus:      bus,
		sub:      sub,
		statusch: make(chan chan<- *apclient.BidEngineStatus),
		orders:   make(map[string]*order),
		drainch:  make(chan *order),
		ordersch: make(chan ordersPage, 1000),
		group:    group,
		cancel:   cancel,
		lc:       lifecycle.New(),
		cfg:      cfg,
		pcfg:     pcfg,
		pass:     providerAttrService,
		waiter:   waiter,
	}

	// register the fetcher before starting run: run waits for the group on exit, and a task
	// added after that wait has returned would outlive the service
	group.Go(func() error {
		return s.ordersFetcher(ctx, aqc)
	})

	go s.lc.WatchContext(pctx)
	go s.run(pctx)

	return s, nil
}

// errOrdersNextKeyRepeated is returned by the orders fetcher when a page names the key it
// was requested with as the key of the following page.
var errOrdersNextKeyRepeated = errors.New("orders pagination: next key repeats the requested key")

// ordersPage is a page of open orders to catch up on.
type ordersPage struct {
	orders []mtypes.OrderID
	// key is the pagination key the page was requested with, nil for the first page.
	key []byte
	// last marks the page the node returned no next key for.
	last bool
}

// service manages the lifecycle and coordination of order processing and bid creation.
type service struct {
	// session provides blockchain client and provider info.
	session session.Session
	// cluster interface to the provider's compute cluster for resource management.
	cluster cluster.Cluster
	// cfg holds configuration parameters for bid engine (pricing, deposits, timeouts etc).
	cfg Config
	// pcfg persists the pagination key the open orders catch-up resumes from.
	pcfg pconfig.Storage

	// bus is the event bus for publishing lease events.
	bus pubsub.Bus
	// sub is the subscriber for receiving blockchain events.
	sub pubsub.Subscriber

	// statusch receives requests for bid engine status.
	statusch chan chan<- *apclient.BidEngineStatus
	// orders tracks all active order processing.
	orders map[string]*order
	// drainch receives completed orders for cleanup.
	drainch chan *order
	// ordersch receives pages of open orders to catch up on from the blockchain.
	ordersch chan ordersPage
	// catchupIncomplete is set once a catch-up order failed to get a monitor.
	catchupIncomplete bool

	group *errgroup.Group
	// cancel holds the cancel function of the service context.
	cancel context.CancelFunc
	// lc manages graceful startup/shutdown.
	lc lifecycle.Lifecycle
	// pass validates provider attributes and signatures.
	pass *providerAttrSignatureService

	// waiter coordinates operator startup dependencies.
	waiter waiter.OperatorWaiter
}

func (s *service) Close() error {
	s.lc.Shutdown(nil)
	return s.lc.Error()
}

func (s *service) Done() <-chan struct{} {
	return s.lc.Done()
}

func (s *service) Status(ctx context.Context) (*apclient.BidEngineStatus, error) {
	ch := make(chan *apclient.BidEngineStatus, 1)

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

func (s *service) StatusV1(ctx context.Context) (*provider.BidEngineStatus, error) {
	res, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}

	return &provider.BidEngineStatus{Orders: res.Orders}, nil
}

func (s *service) updateOrderManagerGauge() {
	orderManagerGauge.Set(float64(len(s.orders)))
}

// ordersFetcher traverses the open orders once and queues them, page by page, for run, which
// creates their monitors. The traversal starts at the pagination key persisted by the previous
// traversal, or at the beginning if there is none, and follows the next keys until the last
// page. Once run has processed the last page it persists the key that page was requested with,
// see processOrdersPage. A traversal from the beginning that fits into one page persists an empty
// key, so the next start traverses from the beginning again. Orders created while the provider
// runs arrive as EventOrderCreated.
func (s *service) ordersFetcher(ctx context.Context, aqc sclient.QueryClient) error {
	nextKey, err := s.pcfg.BidEngine().GetOrdersNextKey(ctx)
	if err != nil {
		s.session.Log().Error("loading orders pagination key", "err", err)
	}

	// resuming is set while the persisted key has not been accepted by the node yet
	resuming := len(nextKey) > 0
	if resuming {
		s.session.Log().Info("resuming open orders catch-up", "pagination_key", fmt.Sprintf("%x", nextKey))
	} else {
		nextKey = nil
		s.session.Log().Info("catching up open orders from the beginning")
	}

	limit := cap(s.ordersch)

loop:
	for {
		// a query sent on a canceled context does not necessarily fail: over ABCI the
		// client does not use the context at all
		if ctx.Err() != nil {
			break loop
		}

		preq := &sdkquery.PageRequest{
			Key:   nextKey,
			Limit: uint64(limit),
		}

		resp, err := aqc.Market().Orders(ctx, &mvbeta.QueryOrdersRequest{
			Filters: mvbeta.OrderFilters{
				State: mvbeta.OrderOpen.String(),
			},
			Pagination: preq,
		})
		if err != nil {
			// a canceled query is no error to report. Check the context rather than the
			// error: canceled before the request reaches gRPC, the query fails with
			// context.Canceled, once in flight with a gRPC status error, which does not
			// match context.Canceled
			if ctx.Err() != nil {
				break loop
			}

			// the node rejects a persisted key it cannot decode, e.g. one saved before a
			// node upgrade changed the key format, on every retry
			if resuming && status.Code(err) == codes.InvalidArgument {
				s.session.Log().Error("orders pagination key rejected, catching up open orders from the beginning",
					"pagination_key", fmt.Sprintf("%x", nextKey), "err", err)

				nextKey = nil
				resuming = false

				continue
			}

			s.session.Log().Error("querying open orders", "err", err)

			select {
			case <-ctx.Done():
				break loop
			case <-time.After(ordersFetchRetryPeriod):
			}

			continue
		}

		resuming = false

		page := ordersPage{
			orders: make([]mtypes.OrderID, 0, len(resp.Orders)),
			key:    nextKey,
		}

		for _, order := range resp.Orders {
			page.orders = append(page.orders, order.ID)
		}

		var pageNextKey []byte
		if resp.Pagination != nil {
			pageNextKey = resp.Pagination.NextKey
		}

		page.last = len(pageNextKey) == 0

		if ctx.Err() != nil {
			break loop
		}

		// a page naming the requested key as the next one would be fetched forever;
		// an empty next key marks the last page and is handled below
		if len(pageNextKey) != 0 && bytes.Equal(pageNextKey, nextKey) {
			return fmt.Errorf("%w: %x", errOrdersNextKeyRepeated, pageNextKey)
		}

		select {
		case s.ordersch <- page:
		case <-ctx.Done():
			break loop
		}

		// empty next key indicates all open orders have been traversed
		if page.last {
			break loop
		}

		nextKey = pageNextKey
	}

	return ctx.Err()
}

// processOrdersPage creates monitors for a page of catch-up orders. An order can also arrive as
// EventOrderCreated while the traversal runs; such an order keeps the monitor it already has.
//
// Once the last page is processed, the key it was requested with is persisted, so a restart
// resumes the catch-up from that page instead of traversing all open orders again. The key is
// persisted here rather than by the fetcher, which runs ahead of run: a restart must not skip
// pages still queued in ordersch. For the same reason it is not persisted if any order of the
// traversal failed to get a monitor.
func (s *service) processOrdersPage(ctx context.Context, page ordersPage) {
	for _, orderID := range page.orders {
		key := mquery.OrderPath(orderID)
		if s.orders[key] != nil {
			s.session.Log().Debug("existing order", "oid", orderID)
			continue
		}

		s.session.Log().Debug("creating catchup order", "oid", orderID)
		order, err := newOrder(s, orderID, s.cfg, s.pass, true)
		if err != nil {
			s.session.Log().Error("creating catchup order", "oid", orderID, "err", err)
			s.catchupIncomplete = true
			continue
		}
		s.orders[key] = order
		s.updateOrderManagerGauge()
	}

	if !page.last {
		return
	}

	if s.catchupIncomplete {
		s.session.Log().Warn("open orders catch-up incomplete, keeping the persisted orders pagination key so a restart traverses the orders again")
		return
	}

	if err := s.pcfg.BidEngine().SetOrdersNextKey(ctx, page.key); err != nil {
		s.session.Log().Error("persisting orders pagination key", "err", err)
		return
	}

	s.session.Log().Info("open orders catch-up complete", "pagination_key", fmt.Sprintf("%x", page.key))
}

// stopOrdersFetcher cancels the orders fetcher and waits for it to return, so it does not
// outlive the service.
func (s *service) stopOrdersFetcher() {
	s.cancel()

	if err := s.group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		s.session.Log().Error("orders fetcher", "err", err)
	}
}

func (s *service) run(ctx context.Context) {
	defer s.lc.ShutdownCompleted()
	defer s.stopOrdersFetcher()
	defer s.sub.Close()
	s.updateOrderManagerGauge()

	// wait for configured operators to be online and responsive before proceeding
	err := s.waiter.WaitForAll(ctx)
	if err != nil {
		s.lc.ShutdownInitiated(err)
		return
	}

	bus := fromctx.MustPubSubFromCtx(ctx)

	signalch := make(chan struct{}, 1)
	trySignal := func() {
		select {
		case signalch <- struct{}{}:
		case <-s.lc.ShutdownRequest():
		default:
		}
	}

	trySignal()

loop:
	for {
		select {
		case shutdownErr := <-s.lc.ShutdownRequest():
			s.session.Log().Debug("received shutdown request", "err", shutdownErr)
			s.lc.ShutdownInitiated(shutdownErr)
			break loop
		case page := <-s.ordersch:
			s.processOrdersPage(ctx, page)
		case ev := <-s.sub.Events():
			switch ev := ev.(type) { // nolint: gocritic
			case *mtypes.EventOrderCreated:
				// new order
				key := mquery.OrderPath(ev.ID)

				s.session.Log().Info("order detected", "oid", ev.ID)

				if order := s.orders[key]; order != nil {
					s.session.Log().Debug("existing order", "oid", ev.ID)
					break
				}

				// create an order object for managing the bid process and order lifecycle
				order, err := newOrder(s, ev.ID, s.cfg, s.pass, false)
				if err != nil {
					s.session.Log().Error("handling order", "oid", ev.ID, "err", err)
					break
				}

				ordersCounter.WithLabelValues("start").Inc()
				s.orders[key] = order
				trySignal()
			}
		case ch := <-s.statusch:
			ch <- &apclient.BidEngineStatus{
				Orders: uint32(len(s.orders)), // nolint: gosec
			}
		case order := <-s.drainch:
			// child done
			key := mquery.OrderPath(order.orderID)
			delete(s.orders, key)
			ordersCounter.WithLabelValues("stop").Inc()
			trySignal()
		case <-signalch:
			bus.Pub(provider.BidEngineStatus{Orders: uint32(len(s.orders))}, []string{ptypes.PubSubTopicBidengineStatus}, tpubsub.WithRetain()) // nolint: gosec
		}
		s.updateOrderManagerGauge()
	}

	// stop queueing catch-up orders that will not be processed
	s.cancel()

	s.pass.lc.ShutdownAsync(nil)

	s.session.Log().Info("draining order monitors", "count", len(s.orders))
	// drain: wait for all order monitors to complete.
	for len(s.orders) > 0 {
		key := mquery.OrderPath((<-s.drainch).orderID)
		delete(s.orders, key)
		s.updateOrderManagerGauge()
	}

	s.session.Log().Debug("waiting on provider attributes service")
	<-s.pass.lc.Done()
	s.session.Log().Info("shutdown complete")
}
