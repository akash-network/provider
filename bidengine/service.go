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
}

// service manages the lifecycle and coordination of order processing and bid creation.
type service struct {
	// session provides blockchain client and provider info.
	session session.Session
	// cluster interface to the provider's compute cluster for resource management.
	cluster cluster.Cluster
	// cfg holds configuration parameters for bid engine (pricing, deposits, timeouts etc).
	cfg Config

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

// ordersFetcher traverses all open orders once and queues them for run, which creates their
// monitors. It always starts from the beginning and persists no progress: monitors live in
// memory and close their bids when the provider shuts down, so after any restart every open
// order needs a monitor again, whatever an earlier run had already seen. Orders created while
// the traversal runs arrive as EventOrderCreated.
//
// With cfg.MaxOrderAge set, open orders older than that are skipped: an order stays open on chain
// until its tenant closes the deployment, so abandoned orders would otherwise be evaluated again
// on every start. Orders this provider has an open bid on are queued whatever their age, so a bid
// left open by a crash is still found and closed.
func (s *service) ordersFetcher(ctx context.Context, aqc sclient.QueryClient) error {
	maxAge := s.cfg.MaxOrderAge
	height := s.session.CreatedAtBlockHeight()

	s.session.Log().Info("catching up open orders", "max_order_age", maxAge)

	if maxAge > 0 {
		bids, _, err := s.traverse(ctx, "open bids", func(ctx context.Context, preq *sdkquery.PageRequest) ([]mtypes.OrderID, int, *sdkquery.PageResponse, error) {
			resp, err := aqc.Market().Bids(ctx, &mvbeta.QueryBidsRequest{
				Filters: mvbeta.BidFilters{
					Provider: s.session.Provider().Owner,
					State:    mvbeta.BidOpen.String(),
				},
				Pagination: preq,
			})
			if err != nil {
				return nil, 0, nil, err
			}

			ids := make([]mtypes.OrderID, 0, len(resp.Bids))
			for _, bid := range resp.Bids {
				ids = append(ids, bid.Bid.ID.OrderID())
			}

			return ids, 0, resp.Pagination, nil
		})
		if err != nil {
			return err
		}

		s.session.Log().Info("caught up orders with an open bid", "orders", bids)
	}

	orders, skipped, err := s.traverse(ctx, "open orders", func(ctx context.Context, preq *sdkquery.PageRequest) ([]mtypes.OrderID, int, *sdkquery.PageResponse, error) {
		resp, err := aqc.Market().Orders(ctx, &mvbeta.QueryOrdersRequest{
			Filters: mvbeta.OrderFilters{
				State: mvbeta.OrderOpen.String(),
			},
			Pagination: preq,
		})
		if err != nil {
			return nil, 0, nil, err
		}

		ids := make([]mtypes.OrderID, 0, len(resp.Orders))
		skipped := 0
		for _, order := range resp.Orders {
			if maxAge > 0 && olderThan(order.CreatedAt, height, maxAge) {
				skipped++
				continue
			}
			ids = append(ids, order.ID)
		}

		return ids, skipped, resp.Pagination, nil
	})
	if err != nil {
		return err
	}

	s.session.Log().Info("caught up open orders", "orders", orders, "skipped_older_than_max_age", skipped)

	return nil
}

// ordersQuery fetches one page of a traversal: the order IDs to queue, how many orders of the
// page were skipped, and the pagination of the response.
type ordersQuery func(context.Context, *sdkquery.PageRequest) ([]mtypes.OrderID, int, *sdkquery.PageResponse, error)

// traverse runs query from the first page to the last and queues the order IDs of every page
// for run. A failed query is retried until ctx is done. It returns the number of orders queued
// and skipped, and ctx.Err() when it was stopped before the last page.
func (s *service) traverse(ctx context.Context, what string, query ordersQuery) (int, int, error) {
	var nextKey []byte
	queued, skipped := 0, 0

	limit := cap(s.ordersch)

	for {
		ids, pageSkipped, pagination, err := query(ctx, &sdkquery.PageRequest{
			Key:   nextKey,
			Limit: uint64(limit),
		})
		if err != nil {
			// check the context rather than the error: gRPC reports cancellation as a status
			// error which does not match context.Canceled
			if ctx.Err() != nil {
				return queued, skipped, ctx.Err()
			}

			s.session.Log().Error("querying "+what, "err", err)

			select {
			case <-ctx.Done():
				return queued, skipped, ctx.Err()
			case <-time.After(ordersFetchRetryPeriod):
			}

			continue
		}

		var pageNextKey []byte
		if pagination != nil {
			pageNextKey = pagination.NextKey
		}

		if ctx.Err() != nil {
			return queued, skipped, ctx.Err()
		}

		// a page naming the requested key as the next one would be fetched forever;
		// an empty next key marks the last page and is handled below
		if len(pageNextKey) != 0 && bytes.Equal(pageNextKey, nextKey) {
			return queued, skipped, fmt.Errorf("%w: %x", errOrdersNextKeyRepeated, pageNextKey)
		}

		select {
		case s.ordersch <- ordersPage{orders: ids}:
		case <-ctx.Done():
			return queued, skipped, ctx.Err()
		}

		queued += len(ids)
		skipped += pageSkipped

		// empty next key indicates all pages have been traversed
		if len(pageNextKey) == 0 {
			return queued, skipped, nil
		}

		nextKey = pageNextKey
	}
}

// processOrdersPage creates monitors for a page of catch-up orders. An order can also arrive as
// EventOrderCreated while the traversal runs; such an order keeps the monitor it already has.
func (s *service) processOrdersPage(page ordersPage) {
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
			continue
		}
		s.orders[key] = order
		s.updateOrderManagerGauge()
	}
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
			s.processOrdersPage(page)
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
