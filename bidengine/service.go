package bidengine

import (
	"context"
	"errors"

	sclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	tpubsub "github.com/troian/pubsub"
	"golang.org/x/sync/errgroup"

	"github.com/boz/go-lifecycle"

	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	provider "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/pubsub"
	mquery "github.com/akash-network/node/x/market/query"

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

// StatusClient interface predefined with Status method
type StatusClient interface {
	Status(context.Context) (*Status, error)
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

	// ctx, cancel := context.WithCancel(pctx)
	// group, _ := errgroup.WithContext(ctx)

	s := &service{
		session:  session,
		cluster:  cluster,
		bus:      bus,
		sub:      sub,
		statusch: make(chan chan<- *Status),
		orders:   make(map[string]*order),
		drainch:  make(chan *order),
		ordersch: make(chan []mtypes.OrderID, 100),
		// group:    group,
		// cancel:   cancel,
		lc:     lifecycle.New(),
		cfg:    cfg,
		pass:   providerAttrService,
		waiter: waiter,
	}

	go s.lc.WatchContext(pctx)
	go s.run(pctx)
	// group.Go(func() error {
	// 	err := s.ordersFetcher(ctx, aqc)
	//
	// 	<-ctx.Done()
	//
	// 	return err
	// })

	return s, nil
}

type service struct {
	session session.Session
	cluster cluster.Cluster
	cfg     Config

	bus pubsub.Bus
	sub pubsub.Subscriber

	statusch chan chan<- *Status
	orders   map[string]*order
	drainch  chan *order
	ordersch chan []mtypes.OrderID

	group  *errgroup.Group
	cancel context.CancelFunc
	lc     lifecycle.Lifecycle
	pass   *providerAttrSignatureService

	waiter waiter.OperatorWaiter
}

func (s *service) Close() error {
	s.lc.Shutdown(nil)
	return s.lc.Error()
}

func (s *service) Done() <-chan struct{} {
	return s.lc.Done()
}

func (s *service) Status(ctx context.Context) (*Status, error) {
	ch := make(chan *Status, 1)

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

func (s *service) ordersFetcher(ctx context.Context, aqc sclient.QueryClient) error {
	var nextKey []byte

	limit := cap(s.ordersch)

loop:
	for {
		preq := &sdkquery.PageRequest{
			Key:   nextKey,
			Limit: uint64(limit),
		}

		resp, err := aqc.Orders(ctx, &mtypes.QueryOrdersRequest{
			Filters: mtypes.OrderFilters{
				State: mtypes.OrderOpen.String(),
			},
			Pagination: preq,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				break
			}

			continue
		}

		if resp.Pagination != nil {
			nextKey = resp.Pagination.NextKey
		}

		var orders []mtypes.OrderID

		for _, order := range resp.Orders {
			orders = append(orders, order.OrderID)
		}

		select {
		case s.ordersch <- orders:
		case <-ctx.Done():
			break loop
		}

		if len(nextKey) == 0 {
			break loop
		}
	}

	return ctx.Err()
}

func (s *service) run(ctx context.Context) {
	defer s.lc.ShutdownCompleted()
	defer s.sub.Close()
	s.updateOrderManagerGauge()

	// wait for configured operators to be online & responsive before proceeding
	err := s.waiter.WaitForAll(ctx)
	if err != nil {
		s.cancel()
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
			s.lc.ShutdownInitiated(nil)
			s.cancel()
			break loop
		case orders := <-s.ordersch:
			for _, orderID := range orders {
				key := mquery.OrderPath(orderID)
				s.session.Log().Debug("creating catchup order", "order", key)
				order, err := newOrder(s, orderID, s.cfg, s.pass, true)
				if err != nil {
					s.session.Log().Error("creating catchup order", "order", key, "err", err)
					continue
				}
				s.orders[key] = order
				s.updateOrderManagerGauge()
			}
		case ev := <-s.sub.Events():
			switch ev := ev.(type) { // nolint: gocritic
			case mtypes.EventOrderCreated:
				// new order
				key := mquery.OrderPath(ev.ID)

				s.session.Log().Info("order detected", "order", key)

				if order := s.orders[key]; order != nil {
					s.session.Log().Debug("existing order", "order", key)
					break
				}

				// create an order object for managing the bid process and order lifecycle
				order, err := newOrder(s, ev.ID, s.cfg, s.pass, false)
				if err != nil {
					s.session.Log().Error("handling order", "order", key, "err", err)
					break
				}

				ordersCounter.WithLabelValues("start").Inc()
				s.orders[key] = order
				trySignal()
			}
		case ch := <-s.statusch:
			ch <- &Status{
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

	_ = s.group.Wait()

	s.pass.lc.ShutdownAsync(nil)

	s.session.Log().Info("draining order monitors", "qty", len(s.orders))
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
