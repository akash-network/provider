package bidengine

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"

	sdkquery "github.com/cosmos/cosmos-sdk/types/query"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
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
		ctx: context.WithValue(context.Background(), fromctx.CtxKeyPersistentConfig, pcfg),
		svc: &service{
			session:  session.New(testutil.Logger(t), nil, provider, 0),
			ordersch: make(chan ordersPage, 1000),
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

// Order monitors live in memory and close their bids when the provider shuts down, so after a
// restart every open order needs a monitor again. A key persisted by an earlier version must
// not make the startup traversal skip the open orders sorting before it.
func Test_OrdersFetcher_IgnoresKeyPersistedByEarlierVersions(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	require.NoError(t, s.pcfg.BidEngine().SetOrdersNextKey(context.Background(), []byte("key-saved-by-earlier-version")))

	oid := testOrderID(t, 1)
	keys := s.captureRequestKeys(orderPage(nil, oid))

	require.NoError(t, requireFetcherExit(t, s.run(s.ctx)))

	require.Equal(t, [][]byte{nil}, *keys, "traversal must start from the beginning")
	require.Equal(t, []mtypes.OrderID{oid}, (<-s.svc.ordersch).orders)
}

// Regression: the traversal persisted a resume key at the last open order, so after a restart
// the provider never caught up on the open orders sorting before it again: neither those whose
// bids it closed on shutdown nor those created while it was down.
func Test_OrdersFetcher_RestartTraversesAllOpenOrders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pconfig.db")
	o1, o2 := testOrderID(t, 1), testOrderID(t, 2)

	store1, err := bbolt.NewBBolt(path)
	require.NoError(t, err)

	first := newOrdersFetcherScaffoldWithStorage(t, store1)
	first.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(nil, o1, o2), nil).Once()

	require.NoError(t, requireFetcherExit(t, first.run(first.ctx)))

	page := <-first.svc.ordersch
	require.Equal(t, []mtypes.OrderID{o1, o2}, page.orders)

	// process the page as run does, without starting order monitors: this is where a resume
	// key used to be persisted, so the restart below would catch a reintroduced checkpoint
	page.orders = nil
	first.svc.processOrdersPage(page)
	require.NoError(t, store1.Close())

	store2, err := bbolt.NewBBolt(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store2.Close() })

	second := newOrdersFetcherScaffoldWithStorage(t, store2)
	keys := second.captureRequestKeys(orderPage(nil, o1, o2))

	require.NoError(t, requireFetcherExit(t, second.run(second.ctx)))

	require.Equal(t, [][]byte{nil}, *keys, "restart must traverse open orders from the beginning")
	require.Equal(t, []mtypes.OrderID{o1, o2}, (<-second.svc.ordersch).orders)
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

	s.svc.processOrdersPage(ordersPage{orders: []mtypes.OrderID{monitored, fresh}})

	require.Equal(t, 1, sub.clones, "a monitor must be started for the new order only")
	require.Same(t, existing, s.svc.orders[mquery.OrderPath(monitored)])
}

// gRPC reports cancellation as a status error (codes.Canceled) which does not match
// context.Canceled via errors.Is. The fetcher must still exit instead of retrying forever.
func Test_OrdersFetcher_ExitsOnGRPCCanceled(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

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
		Return(nil, status.FromContextError(context.Canceled).Err())

	err := requireFetcherExit(t, s.run(ctx))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []mtypes.OrderID{oid}, (<-s.svc.ordersch).orders)
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
