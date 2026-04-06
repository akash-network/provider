package bidengine

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tpubsub "github.com/troian/pubsub"
	"pkg.akt.dev/go/sdkutil"

	sdk "github.com/cosmos/cosmos-sdk/types"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	providermocks "pkg.akt.dev/go/mocks/node/client/provider"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	"pkg.akt.dev/go/node/types/constants"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	cmocks "github.com/akash-network/provider/mocks/cluster"
	clmocks "github.com/akash-network/provider/mocks/cluster/types"
	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
)

// screenBidScaffold holds mocks and the service used for ScreenBid tests.
type screenBidScaffold struct {
	service       Service
	cluster       *cmocks.Cluster
	pricing       *mockPricingStrategy
	cancel        context.CancelFunc
	providerAddr  sdk.AccAddress
	providerMocks *providermocks.QueryClient
}

// mockPricingStrategy is a controllable pricing strategy for tests.
type mockPricingStrategy struct {
	price sdk.DecCoin
	err   error
}

func (m *mockPricingStrategy) CalculatePrice(_ context.Context, _ Request) (sdk.DecCoin, error) {
	return m.price, m.err
}

var _ BidPricingStrategy = (*mockPricingStrategy)(nil)

// setupScreenBidService creates a bidengine.Service wired with mocks suitable for ScreenBid testing.
// The provider attribute service is started and provider attributes are fetched before returning.
func setupScreenBidService(t *testing.T, providerAttrs attrtypes.Attributes, pricing *mockPricingStrategy) *screenBidScaffold {
	t.Helper()

	homeDir, err := os.MkdirTemp("", "bidengine-screen-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(homeDir) })

	providerAddr := testutil.AccAddress(t)

	queryMocks := &clientmocks.QueryClient{}
	deplMocks := &deploymentmocks.QueryClient{}
	marketMocks := &marketmocks.QueryClient{}
	provMocks := &providermocks.QueryClient{}

	marketMocks.On("Orders", mock.Anything, mock.Anything).Return(&mvbeta.QueryOrdersResponse{}, nil)
	provMocks.On("Provider", mock.Anything, mock.Anything).Return(&ptypes.QueryProviderResponse{
		Provider: ptypes.Provider{
			Owner:      providerAddr.String(),
			Attributes: providerAttrs,
		},
	}, nil)

	queryMocks.On("ClientContext").Return(sdkclient.Context{HomeDir: homeDir}, nil)
	queryMocks.On("Deployment").Return(deplMocks, nil)
	queryMocks.On("Market").Return(marketMocks, nil)
	queryMocks.On("Provider").Return(provMocks, nil)

	txMocks := &clientmocks.TxClient{}
	txMocks.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).Return(&sdk.Result{}, nil)

	clientMocks := &clientmocks.Client{}
	clientMocks.On("Query").Return(queryMocks)
	clientMocks.On("Tx").Return(txMocks)

	myProvider := &ptypes.Provider{
		Owner:      providerAddr.String(),
		Attributes: providerAttrs,
	}
	mySession := session.New(testutil.Logger(t), clientMocks, myProvider, 1)

	bus := pubsub.NewBus()
	clusterMock := &cmocks.Cluster{}

	cfg := Config{
		PricingStrategy: pricing,
		Deposit:         mvbeta.DefaultBidMinDeposit,
		MaxGroupVolumes: constants.DefaultMaxGroupVolumes,
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))

	svc, err := NewService(ctx, queryMocks, mySession, clusterMock, bus, waiter.NewNullWaiter(), cfg)
	require.NoError(t, err)
	require.NotNil(t, svc)

	t.Cleanup(func() {
		cancel()
		_ = svc.Close()
		<-svc.Done()
	})

	// Wait for provider attributes to be fetched.
	// The providerAttrSignatureService fetches asynchronously; give it time.
	require.Eventually(t, func() bool {
		svcImpl := svc.(*service)
		_, err := svcImpl.pass.GetAttributes()
		return err == nil
	}, 10*time.Second, 100*time.Millisecond, "timed out waiting for provider attributes to be fetched")

	return &screenBidScaffold{
		service:       svc,
		cluster:       clusterMock,
		pricing:       pricing,
		cancel:        cancel,
		providerAddr:  providerAddr,
		providerMocks: provMocks,
	}
}

func Test_ScreenBid_AllStepsPass(t *testing.T) {
	expectedPrice := sdk.NewInt64DecCoin(sdkutil.DenomUact, 42)
	pricing := &mockPricingStrategy{price: expectedPrice}
	scaffold := setupScreenBidService(t, nil, pricing)

	gspec := validGroupSpec()

	allocatedResources := gspec.Resources
	mockRG := &clmocks.ReservationGroup{}
	mockRG.On("GetAllocatedResources").Return(allocatedResources)
	mockRG.On("ClusterParams").Return(nil)

	scaffold.cluster.On("DryRunReserve", mock.Anything, mock.Anything).Return(mockRG, nil)

	result, err := scaffold.service.ScreenBid(context.Background(), gspec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Passed)
	require.Empty(t, result.Reasons)
	require.Equal(t, expectedPrice, result.Price)
	require.Equal(t, allocatedResources, result.AllocatedResources)

	scaffold.cluster.AssertCalled(t, "DryRunReserve", mock.Anything, mock.Anything)
}

func Test_ScreenBid_EligibilityFails(t *testing.T) {
	pricing := &mockPricingStrategy{price: sdk.NewInt64DecCoin(sdkutil.DenomUact, 1)}
	// Provider has "region=eu"
	providerAttrs := attrtypes.Attributes{{Key: "region", Value: "eu"}}
	scaffold := setupScreenBidService(t, providerAttrs, pricing)

	gspec := validGroupSpec()
	// Order requires "region=us" — doesn't match provider
	gspec.Requirements.Attributes = attrtypes.Attributes{{Key: "region", Value: "us"}}

	result, err := scaffold.service.ScreenBid(context.Background(), gspec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Passed)
	require.Contains(t, result.Reasons, "incompatible provider attributes")

	// DryRunReserve should NOT have been called
	scaffold.cluster.AssertNotCalled(t, "DryRunReserve", mock.Anything, mock.Anything)
}

func Test_ScreenBid_DryRunReserveFails(t *testing.T) {
	pricing := &mockPricingStrategy{price: sdk.NewInt64DecCoin(sdkutil.DenomUact, 1)}
	scaffold := setupScreenBidService(t, nil, pricing)

	gspec := validGroupSpec()

	reserveErr := errors.New("not enough GPUs")
	scaffold.cluster.On("DryRunReserve", mock.Anything, mock.Anything).Return(nil, reserveErr)

	result, err := scaffold.service.ScreenBid(context.Background(), gspec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Passed)
	require.Len(t, result.Reasons, 1)
	require.Contains(t, result.Reasons[0], "insufficient capacity")
	require.Contains(t, result.Reasons[0], "not enough GPUs")
}

func Test_ScreenBid_PricingFails(t *testing.T) {
	pricingErr := errors.New("pricing script crashed")
	pricing := &mockPricingStrategy{err: pricingErr}
	scaffold := setupScreenBidService(t, nil, pricing)

	gspec := validGroupSpec()

	allocatedResources := gspec.Resources
	mockRG := &clmocks.ReservationGroup{}
	mockRG.On("GetAllocatedResources").Return(allocatedResources)
	mockRG.On("ClusterParams").Return(nil)

	scaffold.cluster.On("DryRunReserve", mock.Anything, mock.Anything).Return(mockRG, nil)

	result, err := scaffold.service.ScreenBid(context.Background(), gspec)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Passed)
	require.Len(t, result.Reasons, 1)
	require.Contains(t, result.Reasons[0], "pricing calculation failed")
	require.Contains(t, result.Reasons[0], "pricing script crashed")
}
