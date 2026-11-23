package bidengine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	feegranttypes "cosmossdk.io/x/feegrant"
	"github.com/cosmos/cosmos-sdk/types/query"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/session"
)

type fakeFeegrantQueryClient struct {
	feegranttypes.QueryClient
	pages    [][]*feegranttypes.Grant
	err      error
	requests []*feegranttypes.QueryAllowancesRequest
}

func (f *fakeFeegrantQueryClient) Allowances(_ context.Context, req *feegranttypes.QueryAllowancesRequest, _ ...grpc.CallOption) (*feegranttypes.QueryAllowancesResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}

	page := len(f.requests) - 1
	res := &feegranttypes.QueryAllowancesResponse{Allowances: f.pages[page], Pagination: &query.PageResponse{}}
	if page < len(f.pages)-1 {
		res.Pagination.NextKey = []byte{byte(page + 1)}
	}

	return res, nil
}

func Test_TenantAllowlistIsDisabledWhenEmpty(t *testing.T) {
	require.False(t, TenantAllowlist{}.IsEnabled())
	require.True(t, TenantAllowlist{Tenants: []string{"akash1tenant"}}.IsEnabled())
	require.True(t, TenantAllowlist{Granters: []string{"akash1granter"}}.IsEnabled())
}

func Test_TenantAllowedWhenAllowlistDisabled(t *testing.T) {
	order, feegrants := setupTenantCheck(t, TenantAllowlist{}, nil, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.True(t, allowed)
	require.Empty(t, feegrants.requests)
}

func Test_TenantAllowedWhenListed(t *testing.T) {
	order, feegrants := setupTenantCheck(t, TenantAllowlist{Tenants: []string{"akash1other", "akash1tenant"}, Granters: []string{"akash1granter"}}, nil, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.True(t, allowed)
	require.Empty(t, feegrants.requests)
}

func Test_TenantDeclinedWhenNotListedAndNoGrantersConfigured(t *testing.T) {
	order, feegrants := setupTenantCheck(t, TenantAllowlist{Tenants: []string{"akash1other"}}, nil, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.False(t, allowed)
	require.Empty(t, feegrants.requests)
}

func Test_TenantAllowedWhenGrantedByListedGranter(t *testing.T) {
	order, feegrants := setupTenantCheck(t, TenantAllowlist{Granters: []string{"akash1other-granter", "akash1granter"}}, [][]*feegranttypes.Grant{
		{{Granter: "akash1unlisted", Grantee: "akash1tenant"}, {Granter: "akash1granter", Grantee: "akash1tenant"}},
	}, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, []*feegranttypes.QueryAllowancesRequest{{Grantee: "akash1tenant"}}, feegrants.requests)
}

func Test_TenantDeclinedWhenGrantedOnlyByUnlistedGranters(t *testing.T) {
	order, _ := setupTenantCheck(t, TenantAllowlist{Granters: []string{"akash1granter"}}, [][]*feegranttypes.Grant{
		{{Granter: "akash1unlisted", Grantee: "akash1tenant"}},
	}, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.False(t, allowed)
}

func Test_TenantDeclinedWhenItHasNoGrants(t *testing.T) {
	order, _ := setupTenantCheck(t, TenantAllowlist{Granters: []string{"akash1granter"}}, [][]*feegranttypes.Grant{{}}, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.False(t, allowed)
}

func Test_TenantAllowedWhenListedGranterIsOnALaterPage(t *testing.T) {
	order, feegrants := setupTenantCheck(t, TenantAllowlist{Granters: []string{"akash1granter"}}, [][]*feegranttypes.Grant{
		{{Granter: "akash1unlisted", Grantee: "akash1tenant"}},
		{{Granter: "akash1granter", Grantee: "akash1tenant"}},
	}, nil)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.NoError(t, err)
	require.True(t, allowed)
	require.Len(t, feegrants.requests, 2)
	require.Equal(t, "akash1tenant", feegrants.requests[1].Grantee)
	require.Equal(t, []byte{1}, feegrants.requests[1].Pagination.Key)
}

func Test_TenantCheckFailsWhenFeeGrantQueryFails(t *testing.T) {
	queryErr := errors.New("rpc unavailable")
	order, _ := setupTenantCheck(t, TenantAllowlist{Granters: []string{"akash1granter"}}, nil, queryErr)

	allowed, err := order.isTenantAllowed(context.Background(), "akash1tenant")

	require.ErrorIs(t, err, queryErr)
	require.ErrorContains(t, err, "akash1tenant")
	require.False(t, allowed)
}

func Test_ShouldntBidForTenantOutsideAllowlist(t *testing.T) {
	cfg := &Config{TenantAllowlist: TenantAllowlist{Tenants: []string{testutil.AccAddress(t).String()}}}
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, cfg, testBidCreatedAt)

	<-order.lc.Done()

	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)
	select {
	case broadcast := <-scaffold.broadcasts:
		require.Failf(t, "unexpected broadcast", "%v", broadcast)
	default:
	}
}

func setupTenantCheck(t *testing.T, allowlist TenantAllowlist, pages [][]*feegranttypes.Grant, queryErr error) (*order, *fakeFeegrantQueryClient) {
	feegrants := &fakeFeegrantQueryClient{pages: pages, err: queryErr}

	queryClient := &clientmocks.QueryClient{}
	queryClient.On("Feegrant").Return(feegrants)

	client := &clientmocks.Client{}
	client.On("Query").Return(queryClient)

	logger := testutil.Logger(t)

	return &order{
		cfg:     Config{TenantAllowlist: allowlist},
		session: session.New(logger, client, &ptypes.Provider{Owner: testutil.AccAddress(t).String()}, 0),
		log:     logger,
	}, feegrants
}
