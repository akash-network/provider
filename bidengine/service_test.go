package bidengine

import (
	"bytes"
	"context"
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

	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/pconfig"
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

func (s *ordersFetcherScaffold) persistedKey(t *testing.T) []byte {
	t.Helper()

	key, err := s.pcfg.BidEngine().GetOrdersNextKey(context.Background())
	require.NoError(t, err)

	return key
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

// Pages queued in ordersch have not been processed yet. Persisting their next key from the
// fetcher would skip those orders if the provider stops before run processes them.
func Test_OrdersFetcher_DoesNotPersistQueuedPages(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	key1 := []byte("page-2-key")
	oid := testOrderID(t, 1)

	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(key1, oid), nil).Once()

	s.market.On("Orders", mock.Anything, pageKey(key1)).
		Return(orderPage(nil), nil).Once()

	err := requireFetcherExit(t, s.run(s.ctx))
	require.NoError(t, err)

	s.market.AssertExpectations(t)
	require.Empty(t, s.persistedKey(t), "next key persisted before its page was processed")
	require.Equal(t, ordersPage{orders: []mtypes.OrderID{oid}, nextKey: key1}, <-s.svc.ordersch)
	require.Equal(t, ordersPage{orders: []mtypes.OrderID{}}, <-s.svc.ordersch)
}

// The next key must be persisted once its page has been processed, not only when the fetcher
// exits, otherwise any non-graceful restart loses all catch-up progress.
func Test_ProcessOrdersPage_PersistsNextKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	key1 := []byte("page-2-key")

	s.svc.processOrdersPage(s.ctx, ordersPage{nextKey: key1})
	require.Equal(t, key1, s.persistedKey(t))

	// last page has no next key; persisted key must not be reset
	s.svc.processOrdersPage(s.ctx, ordersPage{})
	require.Equal(t, key1, s.persistedKey(t))
}

// gRPC reports cancellation as a status error (codes.Canceled) which does not match
// context.Canceled via errors.Is. The fetcher must still exit instead of retrying forever.
func Test_OrdersFetcher_ExitsOnGRPCCanceled(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()

	key1 := []byte("page-2-key")

	s.market.On("Orders", mock.Anything, pageKey(nil)).
		Return(orderPage(key1, testOrderID(t, 1)), nil).Once()

	s.market.On("Orders", mock.Anything, pageKey(key1)).
		Run(func(_ mock.Arguments) {
			cancel()
		}).
		Return(nil, status.FromContextError(context.Canceled).Err())

	err := requireFetcherExit(t, s.run(ctx))
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, key1, (<-s.svc.ordersch).nextKey)
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

func Test_OrdersFetcher_ResumesFromPersistedKey(t *testing.T) {
	s := newOrdersFetcherScaffold(t)

	key := []byte("resume-key")
	require.NoError(t, s.pcfg.BidEngine().SetOrdersNextKey(context.Background(), key))

	s.market.On("Orders", mock.Anything, pageKey(key)).
		Return(orderPage(nil, testOrderID(t, 1)), nil).Once()

	err := requireFetcherExit(t, s.run(s.ctx))
	require.NoError(t, err)

	s.market.AssertExpectations(t)
	// last page has no next key; persisted key must not be reset
	require.Equal(t, key, s.persistedKey(t))
}
