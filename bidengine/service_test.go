package bidengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/boz/go-lifecycle"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdkquery "github.com/cosmos/cosmos-sdk/types/query"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"
	mquery "pkg.akt.dev/node/v2/x/market/query"

	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/logging/logtest"
	"github.com/akash-network/provider/tools/pconfig"
	"github.com/akash-network/provider/tools/pconfig/bbolt"
	"github.com/akash-network/provider/tools/pconfig/memory"
)

const ordersFetcherTestTimeout = 5 * time.Second

type ordersFetcherScaffold struct {
	ctx    context.Context
	svc    *service
	pcfg   pconfig.Storage
	aqc    *clientmocks.QueryClient
	market *marketmocks.QueryClient
}

func newOrdersFetcherScaffold(t *testing.T) *ordersFetcherScaffold {
	t.Helper()

	pcfg, err := memory.NewMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pcfg.Close() })

	return newOrdersFetcherScaffoldWithStorage(t, pcfg)
}

func newOrdersFetcherScaffoldWithStorage(t *testing.T, pcfg pconfig.Storage) *ordersFetcherScaffold {
	t.Helper()

	market := &marketmocks.QueryClient{}
	aqc := &clientmocks.QueryClient{}
	aqc.On("Market").Return(market)

	provider := &ptypes.Provider{Owner: testutil.AccAddress(t).String()}

	return &ordersFetcherScaffold{
		ctx: context.Background(),
		svc: &service{
			session:  session.New(testutil.Logger(t), nil, provider, 0),
			ordersch: make(chan ordersPage, 1000),
			pcfg:     pcfg,
		},
		pcfg:   pcfg,
		aqc:    aqc,
		market: market,
	}
}

func (s *ordersFetcherScaffold) run(ctx context.Context) <-chan error {
	errch := make(chan error, 1)
	go func() {
		errch <- s.svc.ordersFetcher(ctx, s.aqc)
	}()

	return errch
}

// persistKey stores key as the orders pagination key persisted by an earlier traversal.
func (s *ordersFetcherScaffold) persistKey(t *testing.T, key []byte) {
	t.Helper()
	require.NoError(t, s.pcfg.BidEngine().SetOrdersNextKey(context.Background(), key))
}

// persistedKey returns the orders pagination key held by the persistent config.
func (s *ordersFetcherScaffold) persistedKey(t *testing.T) []byte {
	t.Helper()

	key, err := s.pcfg.BidEngine().GetOrdersNextKey(context.Background())
	require.NoError(t, err)

	return key
}

// processPages processes the n queued pages as run does, without starting order monitors.
func (s *ordersFetcherScaffold) processPages(t *testing.T, n int) {
	t.Helper()

	require.Len(t, s.svc.ordersch, n)

	for range n {
		page := <-s.svc.ordersch
		page.orders = nil
		s.svc.processOrdersPage(s.ctx, page)
	}
}

// captureRequestKeys answers one Orders query per response, in order, and records the
// pagination key each query was made with.
func (s *ordersFetcherScaffold) captureRequestKeys(responses ...*mvbeta.QueryOrdersResponse) *[][]byte {
	keys := &[][]byte{}

	for _, resp := range responses {
		s.market.On("Orders", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				req := args.Get(1).(*mvbeta.QueryOrdersRequest)
				*keys = append(*keys, req.Pagination.Key)
			}).
			Return(resp, nil).Once()
	}

	return keys
}

func pageKey(key []byte) interface{} {
	return mock.MatchedBy(func(req *mvbeta.QueryOrdersRequest) bool {
		return req.Pagination != nil && bytes.Equal(req.Pagination.Key, key)
	})
}

func orderPage(nextKey []byte, ids ...mtypes.OrderID) *mvbeta.QueryOrdersResponse {
	resp := &mvbeta.QueryOrdersResponse{
		Pagination: &sdkquery.PageResponse{NextKey: nextKey},
	}

	for _, id := range ids {
		resp.Orders = append(resp.Orders, mvbeta.Order{ID: id})
	}

	return resp
}

func testOrderID(t *testing.T, seq uint32) mtypes.OrderID {
	t.Helper()
	return mtypes.MakeOrderID(dtypes.MakeGroupID(testutil.DeploymentID(t), 1), seq)
}

func requireFetcherExit(t *testing.T, errch <-chan error) error {
	t.Helper()

	select {
	case err := <-errch:
		return err
	case <-time.After(ordersFetcherTestTimeout):
		t.Fatal("orders fetcher did not exit")
	}

	return nil
}

// cloneCountingSub stands in for the service's bus subscription. Every order monitor starts by
// cloning it, so Clone calls count the monitors started. Clone fails, so none actually runs.
type cloneCountingSub struct {
	clones int
}

func (s *cloneCountingSub) Events() <-chan pubsub.Event { return nil }
func (s *cloneCountingSub) Close()                      {}
func (s *cloneCountingSub) Done() <-chan struct{}       { return nil }

func (s *cloneCountingSub) Clone() (pubsub.Subscriber, error) {
	s.clones++
	return nil, errors.New("order monitors are not started in this test")
}

// stubSub stands in for the service's bus subscription where order monitors must start. Its
// clones see no events.
type stubSub struct{}

func (stubSub) Events() <-chan pubsub.Event       { return nil }
func (stubSub) Close()                            {}
func (stubSub) Done() <-chan struct{}             { return nil }
func (stubSub) Clone() (pubsub.Subscriber, error) { return stubSub{}, nil }

// Within a run the traversal follows the node's next keys until the last page.
func Test_OrdersFetcher_FollowsNextKeys(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	key1 := []byte("page-2-key")
	o1, o2 := testOrderID(t, 1), testOrderID(t, 2)

	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(key1, o1), nil).Once()

	s.market.On("Orders", mock.Anything, pageKey(key1)).
		Return(orderPage(nil, o2), nil).Once()

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))

	s.market.AssertExpectations(t)
	require.Equal(t, []mtypes.OrderID{o1}, (<-s.svc.ordersch).orders)
	require.Equal(t, []mtypes.OrderID{o2}, (<-s.svc.ordersch).orders)
}

// A restart resumes the catch-up at the key persisted by the previous traversal instead of
// traversing all open orders from the beginning.
func Test_OrdersFetcher_ResumesFromPersistedKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	persisted := []byte("persisted-key")
	s.persistKey(t, persisted)

	oid := testOrderID(t, 1)
	keys := s.captureRequestKeys(orderPage(nil, oid))

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))

	require.Equal(t, [][]byte{persisted}, *keys, "traversal must start at the persisted key")
	require.Equal(t, []mtypes.OrderID{oid}, (<-s.svc.ordersch).orders)
}

// Once run has processed the last page, the key that page was requested with is persisted, so a
// restart resumes from that page instead of traversing all open orders again. Uses the storage
// the provider uses (bbolt), closed and reopened between the runs.
func Test_OrdersFetcher_RestartResumesFromLastPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pconfig.db")
	lastPageKey := []byte("last-page-key")
	o1, o2 := testOrderID(t, 1), testOrderID(t, 2)

	store1, err := bbolt.NewBBolt(path)
	require.NoError(t, err)

	first := newOrdersFetcherScaffoldWithStorage(t, store1)
	first.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(lastPageKey, o1), nil).Once()
	first.market.On("Orders", mock.Anything, pageKey(lastPageKey)).
		Return(orderPage(nil, o2), nil).Once()

	require.NoError(t, requireFetcherExit(t, first.run(first.ctx)))
	first.processPages(t, 2)
	require.NoError(t, store1.Close())

	store2, err := bbolt.NewBBolt(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store2.Close() })

	second := newOrdersFetcherScaffoldWithStorage(t, store2)
	keys := second.captureRequestKeys(orderPage(nil, o2))

	require.NoError(t, requireFetcherExit(t, second.run(second.ctx)))

	require.Equal(t, [][]byte{lastPageKey}, *keys, "restart must resume from the last page")
}

// The fetcher runs ahead of run, which first waits for the operators to come up. A key persisted
// before run has processed the queued pages would make a restart skip the orders on them.
func Test_OrdersFetcher_DoesNotPersistQueuedPages(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	persisted := []byte("persisted-key")
	s.persistKey(t, persisted)

	lastPageKey := []byte("last-page-key")
	s.market.On("Orders", mock.Anything, pageKey(persisted)).
		Return(orderPage(lastPageKey, testOrderID(t, 1)), nil).Once()
	s.market.On("Orders", mock.Anything, pageKey(lastPageKey)).
		Return(orderPage(nil, testOrderID(t, 2)), nil).Once()

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))

	require.Len(t, s.svc.ordersch, 2)
	require.Equal(t, persisted, s.persistedKey(t), "queued pages must not move the persisted key")
}

// A traversal interrupted before its last page keeps the key persisted by the previous one, so a
// restart traverses again the pages this one did not reach.
func Test_OrdersFetcher_InterruptedTraversalKeepsPersistedKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	persisted := []byte("persisted-key")
	s.persistKey(t, persisted)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	key2, key3 := []byte("page-2-key"), []byte("page-3-key")
	s.market.On("Orders", mock.Anything, pageKey(persisted)).
		Return(orderPage(key2, testOrderID(t, 1)), nil).Once()
	s.market.On("Orders", mock.Anything, pageKey(key2)).
		Return(orderPage(key3, testOrderID(t, 2)), nil).Once()
	s.market.On("Orders", mock.Anything, pageKey(key3)).
		Run(func(_ mock.Arguments) {
			cancel()
		}).
		Return(nil, status.FromContextError(context.Canceled).Err()).Once()

	require.ErrorIs(t, requireFetcherExit(t, s.run(ctx)), context.Canceled)
	s.processPages(t, 2)

	require.Equal(t, persisted, s.persistedKey(t), "an interrupted traversal must not move the persisted key")
}

// An order whose monitor failed to start, e.g. while the bus shuts down, must be traversed again
// after a restart, so its traversal must keep the key persisted by the previous one.
func Test_ProcessOrdersPage_KeepsPersistedKeyWhenMonitorFailsToStart(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	sub := &cloneCountingSub{}
	s.svc.sub = sub

	persisted := []byte("persisted-key")
	s.persistKey(t, persisted)

	lastPageKey := []byte("last-page-key")
	s.market.On("Orders", mock.Anything, pageKey(persisted)).
		Return(orderPage(lastPageKey, testOrderID(t, 1)), nil).Once()
	s.market.On("Orders", mock.Anything, pageKey(lastPageKey)).
		Return(orderPage(nil), nil).Once()

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))
	require.Len(t, s.svc.ordersch, 2)

	s.svc.processOrdersPage(s.ctx, <-s.svc.ordersch)
	require.Equal(t, 1, sub.clones, "the order on the first page must have tried to start a monitor")

	s.svc.processOrdersPage(s.ctx, <-s.svc.ordersch)

	require.Equal(t, persisted, s.persistedKey(t))
}

// Orders that get monitors do not keep their traversal's key from being persisted: once the last
// page is processed, the key it was requested with is persisted. Each monitor here stops at its
// first query.
func Test_ProcessOrdersPage_PersistsKeyAfterStartingMonitors(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	monitorDeployment := &deploymentmocks.QueryClient{}
	monitorDeployment.On("Group", mock.Anything, mock.Anything).
		Return(nil, errors.New("group query is not served in this test"))

	monitorMarket := &marketmocks.QueryClient{}
	monitorMarket.On("Bid", mock.Anything, mock.Anything).
		Return(nil, errors.New("bid query is not served in this test"))

	monitorQuery := &clientmocks.QueryClient{}
	monitorQuery.On("Deployment").Return(monitorDeployment)
	monitorQuery.On("Market").Return(monitorMarket)

	monitorClient := &clientmocks.Client{}
	monitorClient.On("Query").Return(monitorQuery)

	provider := &ptypes.Provider{Owner: testutil.AccAddress(t).String()}
	s.svc.session = session.New(logtest.New(), monitorClient, provider, 0)
	s.svc.sub = stubSub{}
	s.svc.lc = lifecycle.New()
	s.svc.orders = make(map[string]*order)
	s.svc.drainch = make(chan *order, 2)

	persisted := []byte("persisted-key")
	s.persistKey(t, persisted)

	lastPageKey := []byte("last-page-key")
	s.market.On("Orders", mock.Anything, pageKey(persisted)).
		Return(orderPage(lastPageKey, testOrderID(t, 1)), nil).Once()
	s.market.On("Orders", mock.Anything, pageKey(lastPageKey)).
		Return(orderPage(nil, testOrderID(t, 2)), nil).Once()

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))
	require.Len(t, s.svc.ordersch, 2)

	s.svc.processOrdersPage(s.ctx, <-s.svc.ordersch)
	s.svc.processOrdersPage(s.ctx, <-s.svc.ordersch)

	require.Len(t, s.svc.orders, 2, "both orders must have got a monitor")
	require.Equal(t, lastPageKey, s.persistedKey(t))

	for range 2 {
		select {
		case <-s.svc.drainch:
		case <-time.After(ordersFetcherTestTimeout):
			t.Fatal("order monitor did not stop")
		}
	}
}

// The node rejects a persisted key it cannot decode, e.g. one saved before a node upgrade changed
// the key format, on every request. The catch-up must start from the beginning instead of
// retrying the key forever, and its last page must replace the rejected key.
func Test_OrdersFetcher_DiscardsRejectedPersistedKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	rejected := []byte("key-saved-by-earlier-version")
	s.persistKey(t, rejected)

	oid := testOrderID(t, 1)
	s.market.On("Orders", mock.Anything, pageKey(rejected)).
		Return(nil, status.Error(codes.InvalidArgument, "invalid pagination key"))
	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(nil, oid), nil).Once()

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))

	s.market.AssertNumberOfCalls(t, "Orders", 2)
	require.Len(t, s.svc.ordersch, 1)

	page := <-s.svc.ordersch
	require.Equal(t, []mtypes.OrderID{oid}, page.orders)

	page.orders = nil
	s.svc.processOrdersPage(s.ctx, page)

	require.Empty(t, s.persistedKey(t), "the last page must replace the rejected key")
}

// Only a persisted key is discarded when the node rejects it. A next key the node returned itself
// is not: restarting the traversal from the beginning would request the same pages in a loop.
func Test_OrdersFetcher_DoesNotRestartWhenNodeRejectsItsNextKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	key2 := []byte("page-2-key")

	var rejectOnce sync.Once
	rejected := make(chan struct{})

	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(key2, testOrderID(t, 1)), nil)
	s.market.On("Orders", mock.Anything, pageKey(key2)).
		Run(func(_ mock.Arguments) {
			rejectOnce.Do(func() { close(rejected) })
		}).
		Return(nil, status.Error(codes.InvalidArgument, "invalid pagination key"))

	errch := s.run(ctx)

	select {
	case <-rejected:
	case <-time.After(ordersFetcherTestTimeout):
		t.Fatal("the next key was not requested")
	}

	// a traversal restarted from the beginning would request the first page again right away; the
	// retry of the rejected key comes only after ordersFetchRetryPeriod
	time.Sleep(100 * time.Millisecond)
	cancel()

	require.ErrorIs(t, requireFetcherExit(t, errch), context.Canceled)
	s.market.AssertNumberOfCalls(t, "Orders", 2)
}

// A context canceled before a query is sent must not send it: the query does not necessarily
// observe the cancellation, e.g. when it runs over ABCI, which does not use the context.
func Test_OrdersFetcher_DoesNotQueryWithCanceledContext(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	s.market.On("Orders", mock.Anything, mock.Anything).
		Return(orderPage(nil, testOrderID(t, 1)), nil).Maybe()

	ctx, cancel := context.WithCancel(s.ctx)
	cancel()

	require.ErrorIs(t, requireFetcherExit(t, s.run(ctx)), context.Canceled)

	s.market.AssertNotCalled(t, "Orders", mock.Anything, mock.Anything)
	require.Empty(t, s.svc.ordersch)
}

// A query failing because its context was canceled reports it as context.Canceled when canceled
// before the request reaches gRPC, and as a gRPC status error, which does not match
// context.Canceled, once in flight. Either way the fetcher must exit without reporting an error.
func Test_OrdersFetcher_ExitsOnCanceledQuery(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "canceled before reaching gRPC",
			err:  fmt.Errorf("querying orders: %w", context.Canceled),
		},
		{
			name: "canceled in flight",
			err:  status.FromContextError(context.Canceled).Err(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newOrdersFetcherScaffold(t)

			rec := logtest.New()
			s.svc.session = session.New(rec, nil, &ptypes.Provider{Owner: testutil.AccAddress(t).String()}, 0)

			ctx, cancel := context.WithCancel(s.ctx)
			defer cancel()

			key1 := []byte("page-2-key")
			oid := testOrderID(t, 1)

			s.market.On("Orders", mock.Anything, pageKey(nil)).
				Return(orderPage(key1, oid), nil).Once()

			s.market.On("Orders", mock.Anything, pageKey(key1)).
				Run(func(_ mock.Arguments) {
					cancel()
				}).
				Return(nil, tt.err)

			err := requireFetcherExit(t, s.run(ctx))
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, []mtypes.OrderID{oid}, (<-s.svc.ordersch).orders)

			for _, entry := range rec.Entries() {
				require.NotEqual(t, "error", entry.Level, "a canceled query is not an error: %q", entry.Msg)
			}
		})
	}
}

// A node returning the requested key as the next key would make the fetcher request the
// same page forever. The fetcher must stop with an error instead of re-sending the page.
func Test_OrdersFetcher_RejectsRepeatedNextKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	key := []byte("stuck-key")

	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(key, testOrderID(t, 1)), nil).Once()

	s.market.On("Orders", mock.Anything, pageKey(key)).
		Return(orderPage(key, testOrderID(t, 2)), nil)

	err := requireFetcherExit(t, s.run(s.ctx))
	require.ErrorIs(t, err, errOrdersNextKeyRepeated)

	// only the first page was sent; the page repeating its own key was rejected
	require.Len(t, s.svc.ordersch, 1)
}

// An order can reach the service both as EventOrderCreated and on a catch-up page while the
// startup traversal runs. A second monitor would bid on the order twice and, once either monitor
// finishes, remove the other one from s.orders.
func Test_ProcessOrdersPage_SkipsOrdersAlreadyMonitored(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	sub := &cloneCountingSub{}
	s.svc.sub = sub
	s.svc.orders = make(map[string]*order)

	monitored, fresh := testOrderID(t, 1), testOrderID(t, 2)
	existing := &order{orderID: monitored}
	s.svc.orders[mquery.OrderPath(monitored)] = existing

	s.svc.processOrdersPage(s.ctx, ordersPage{orders: []mtypes.OrderID{monitored, fresh}})

	require.Equal(t, 1, sub.clones, "a monitor must be started for the new order only")
	require.Same(t, existing, s.svc.orders[mquery.OrderPath(monitored)])
}

// The catch-up resumes from the pagination key it persists, so the service must not start
// without the persistent config. The check precedes any use of the other arguments.
func Test_NewService_RequiresPersistentConfig(t *testing.T) {
	_, err := NewService(context.Background(), nil, nil, nil, nil, nil, Config{})
	require.ErrorIs(t, err, fromctx.ErrNotFound)
}
