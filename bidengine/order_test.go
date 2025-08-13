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

	sdkmath "cosmossdk.io/math"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"

	clientmocks "pkg.akt.dev/go/mocks/node/client"
	deploymentmocks "pkg.akt.dev/go/mocks/node/client/deployment"
	marketmocks "pkg.akt.dev/go/mocks/node/client/market"
	providermocks "pkg.akt.dev/go/mocks/node/client/provider"
	audittypes "pkg.akt.dev/go/node/audit/v1"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	dvbeta "pkg.akt.dev/go/node/deployment/v1beta4"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	"pkg.akt.dev/go/node/types/constants"
	rtypes "pkg.akt.dev/go/node/types/resources/v1beta4"
	"pkg.akt.dev/go/testutil"
	"pkg.akt.dev/go/util/pubsub"

	cmocks "github.com/akash-network/provider/mocks/cluster"
	clmocks "github.com/akash-network/provider/mocks/cluster/types"
	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/session"
	"github.com/akash-network/provider/tools/fromctx"
)

type orderTestScaffold struct {
	orderID           mtypes.OrderID
	groupID           dtypes.GroupID
	testBus           pubsub.Bus
	testAddr          sdk.AccAddress
	deploymentID      dtypes.DeploymentID
	bidID             *mtypes.BidID
	client            *clientmocks.Client
	queryClient       *clientmocks.QueryClient
	deplMocks         *deploymentmocks.QueryClient
	marketMocks       *marketmocks.QueryClient
	providerMocks     *providermocks.QueryClient
	txClient          *clientmocks.TxClient
	cluster           *cmocks.Cluster
	broadcasts        chan []sdk.Msg
	reserveCallNotify chan int
}

type testBidPricingStrategy int64

type alwaysFailsBidPricingStrategy struct {
	failure error
}

var _ BidPricingStrategy = (*testBidPricingStrategy)(nil)
var _ BidPricingStrategy = (*alwaysFailsBidPricingStrategy)(nil)

func makeMocks(s *orderTestScaffold) {
	groupResult := &dvbeta.QueryGroupResponse{}
	groupResult.Group.GroupSpec.Name = "testGroupName"
	groupResult.Group.GroupSpec.Resources = make(dvbeta.ResourceUnits, 1)

	cpu := rtypes.CPU{}
	cpu.Units = rtypes.NewResourceValue(uint64(dvbeta.GetValidationConfig().Unit.Min.CPU))

	gpu := rtypes.GPU{}
	gpu.Units = rtypes.NewResourceValue(uint64(dvbeta.GetValidationConfig().Unit.Min.GPU))

	memory := rtypes.Memory{}
	memory.Quantity = rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Memory)

	storage := rtypes.Volumes{
		rtypes.Storage{
			Quantity: rtypes.NewResourceValue(dvbeta.GetValidationConfig().Unit.Min.Storage),
		},
	}

	clusterResources := rtypes.Resources{
		ID:      1,
		CPU:     &cpu,
		GPU:     &gpu,
		Memory:  &memory,
		Storage: storage,
	}
	price := sdk.NewInt64DecCoin(testutil.CoinDenom, 23)
	resource := dvbeta.ResourceUnit{
		Resources: clusterResources,
		Count:     2,
		Price:     price,
	}

	groupResult.Group.GroupSpec.Resources[0] = resource

	homeDir, _ := os.MkdirTemp("", "akash-network-test-*")

	queryMocks := &clientmocks.QueryClient{}
	deplMocks := &deploymentmocks.QueryClient{}
	marketMocks := &marketmocks.QueryClient{}
	providerMocks := &providermocks.QueryClient{}

	deplMocks.On("Group", mock.Anything, mock.Anything).Return(groupResult, nil)
	marketMocks.On("Orders", mock.Anything, mock.Anything).Return(&mvbeta.QueryOrdersResponse{}, nil)
	providerMocks.On("Provider", mock.Anything, mock.Anything).Return(&ptypes.QueryProviderResponse{}, nil)

	queryMocks.On("ClientContext").Return(sdkclient.Context{HomeDir: homeDir}, nil)
	queryMocks.On("Deployment").Return(deplMocks, nil)
	queryMocks.On("Market").Return(marketMocks, nil)
	queryMocks.On("Provider").Return(providerMocks, nil)

	txMocks := &clientmocks.TxClient{}
	s.broadcasts = make(chan []sdk.Msg, 1)

	txMocks.On("BroadcastMsgs", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		s.broadcasts <- args.Get(1).([]sdk.Msg)
	}).Return(&sdk.Result{}, nil)

	clientMocks := &clientmocks.Client{}
	clientMocks.On("Query").Return(queryMocks)
	clientMocks.On("Tx").Return(txMocks)

	s.client = clientMocks
	s.queryClient = queryMocks
	s.deplMocks = deplMocks
	s.marketMocks = marketMocks
	s.providerMocks = providerMocks

	s.txClient = txMocks

	mockReservation := &clmocks.Reservation{}
	mockReservation.On("OrderID").Return(s.orderID)
	mockReservation.On("Resources").Return(groupResult.Group)
	mockReservation.On("GetAllocatedResources").Return(groupResult.Group.GroupSpec.Resources)

	s.cluster = &cmocks.Cluster{}
	s.reserveCallNotify = make(chan int, 1)
	s.cluster.On("Reserve", s.orderID, &(groupResult.Group)).Run(func(_ mock.Arguments) {
		s.reserveCallNotify <- 0
		time.Sleep(time.Second) // add a delay before returning response, to test race conditions
	}).Return(mockReservation, nil)

	s.cluster.On("Unreserve", s.orderID, mock.Anything).Return(nil)
}

type nullProviderAttrSignatureService struct{}

func (nullProviderAttrSignatureService) GetAuditorAttributeSignatures(_ string) (audittypes.AuditedProviders, error) {
	return nil, nil // Return no attributes & no error
}

func (nullProviderAttrSignatureService) GetAttributes() (attrtypes.Attributes, error) {
	return nil, nil // Return no attributes & no error
}

const testBidCreatedAt = 1234556789

func makeOrderForTest(
	t *testing.T,
	checkForExistingBid bool,
	bidState mvbeta.Bid_State,
	pricing BidPricingStrategy,
	callerConfig *Config,
	sessionHeight int64,
) (*order, orderTestScaffold, <-chan int) {
	if pricing == nil {
		pricing = testBidPricingStrategy(1)
		require.NotNil(t, pricing)
	}

	var scaffold orderTestScaffold
	scaffold.deploymentID = testutil.DeploymentID(t)
	scaffold.groupID = dtypes.MakeGroupID(scaffold.deploymentID, 2)
	scaffold.orderID = mtypes.MakeOrderID(scaffold.groupID, 1356326)

	myLog := testutil.Logger(t)

	makeMocks(&scaffold)

	scaffold.testAddr = testutil.AccAddress(t)

	myProvider := &ptypes.Provider{
		Owner:      scaffold.testAddr.String(),
		HostURI:    "",
		Attributes: nil,
	}
	mySession := session.New(myLog, scaffold.client, myProvider, sessionHeight)

	scaffold.testBus = pubsub.NewBus()
	var cfg Config
	if callerConfig != nil {
		cfg = *callerConfig // Copy values from caller
	}
	// Overwrite some with stuff built-in this function
	cfg.PricingStrategy = pricing
	cfg.Deposit = mvbeta.DefaultBidMinDeposit
	cfg.MaxGroupVolumes = constants.DefaultMaxGroupVolumes

	ctx := context.Background()
	ctx = context.WithValue(ctx, fromctx.CtxKeyPubSub, tpubsub.New(ctx, 1000))
	myService, err := NewService(ctx, scaffold.queryClient, mySession, scaffold.cluster, scaffold.testBus, waiter.NewNullWaiter(), cfg)
	require.NoError(t, err)
	require.NotNil(t, myService)

	serviceCast := myService.(*service)

	if checkForExistingBid {
		bidID := mtypes.MakeBidID(scaffold.orderID, mySession.Provider().Address())
		scaffold.bidID = &bidID
		queryBidRequest := &mvbeta.QueryBidRequest{
			ID: bidID,
		}
		response := &mvbeta.QueryBidResponse{
			Bid: mvbeta.Bid{
				ID:        bidID,
				State:     bidState,
				Price:     sdk.NewInt64DecCoin(testutil.CoinDenom, int64(testutil.RandRangeInt(100, 1000))),
				CreatedAt: testBidCreatedAt,
			},
		}
		scaffold.marketMocks.On("Bid", mock.Anything, queryBidRequest).Return(response, nil)
	}

	reservationFulfilledNotify := make(chan int, 1)
	order, err := newOrderInternal(serviceCast, scaffold.orderID, cfg, nullProviderAttrSignatureService{}, checkForExistingBid, reservationFulfilledNotify)

	require.NoError(t, err)
	require.NotNil(t, order)

	return order, scaffold, reservationFulfilledNotify
}

func requireMsgType[T any](t *testing.T, res interface{}) T {
	t.Helper()

	require.IsType(t, []sdk.Msg{}, res)

	msgs := res.([]sdk.Msg)
	require.Len(t, msgs, 1)
	require.IsType(t, *new(T), msgs[0])

	return msgs[0].(T)
}

func Test_BidOrderAndUnreserve(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mvbeta.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.ID.OrderID(), scaffold.orderID)

	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.GreaterOrEqual(t, priceAmount.TruncateInt64(), int64(1))
	require.Less(t, priceAmount.TruncateInt64(), int64(100))

	// After the broadcast call shut the thing down
	// and then check what was broadcast
	order.lc.Shutdown(nil)

	// Should have called unreserve once, nothing happened after the bid
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func Test_BidOrderAndUnreserveOnTimeout(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, &Config{
		BidTimeout: 5 * time.Second,
	}, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mvbeta.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.ID.OrderID(), scaffold.orderID)

	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.True(t, priceAmount.GT(sdkmath.LegacyNewDec(0)))
	require.True(t, priceAmount.LT(sdkmath.LegacyNewDec(100)))

	// After the broadcast call, the timeout should take effect
	// and then close the bid, unreserving capacity in the process
	broadcast = testutil.ChannelWaitForValue(t, scaffold.broadcasts)

	_ = requireMsgType[*mvbeta.MsgCloseBid](t, broadcast)

	// After the broadcast call shut down happens automatically
	order.lc.Shutdown(nil)
	select {
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting on shutdown")
	case <-order.lc.Done():
		break
	}

	// Should have called unreserve once
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func Test_BidOrderPriceTooHigh(t *testing.T) {
	pricing := testBidPricingStrategy(9999999999)
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, pricing, nil, testBidCreatedAt)

	select {
	case <-order.lc.Done(): // Should stop on its own

	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting in test")
	}
	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	select {
	case <-scaffold.broadcasts:
		t.Fatal("should not have broadcast anything")
	default:
	}

	// Should have called unreserve once, nothing happened after the bid
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)

}

func Test_BidOrderAndThenClosedUnreserve(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once at this point
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	ev := &mtypes.EventOrderClosed{
		ID: scaffold.orderID,
	}
	err := scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// Wait for this to complete. An order close event has happened so it stops
	// on its own
	<-order.lc.Done()

	// Should have called unreserve once
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func Test_OrderCloseBeforeReserveReturn(t *testing.T) {
	order, scaffold, reservationFulfilledNotify := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	testutil.ChannelWaitForValue(t, scaffold.reserveCallNotify)
	// Should have called reserve once at this point
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// reservationFulfilledNotify channel shouldn't have got any value yet because the Reserve call
	// returns after a delay of one second
	select {
	case <-reservationFulfilledNotify:
		t.Fatal("reservation shouldn't have been fulfilled")
	default:
	}

	// close the order before Reserve call returns
	ev := &mtypes.EventOrderClosed{
		ID: scaffold.orderID,
	}
	err := scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// reservationFulfilledNotify channel can't get any value now because the order close event
	// should take priority
	select {
	case <-reservationFulfilledNotify:
		t.Fatal("reservation shouldn't have been fulfilled")
	default:
	}

	// Wait for this to complete. An order close event has happened so it stops
	// on its own
	<-order.lc.Done()

	// Should have called unreserve once
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func Test_BidOrderAndThenLeaseCreated(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	// Wait for the first broadcast
	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	createBidMsg := requireMsgType[*mvbeta.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.ID.OrderID(), scaffold.orderID)
	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.GreaterOrEqual(t, priceAmount.TruncateInt64(), int64(1))
	require.Less(t, priceAmount.TruncateInt64(), int64(100))

	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(scaffold.orderID, scaffold.testAddr))

	ev := &mtypes.EventLeaseCreated{
		ID:    leaseID,
		Price: testutil.AkashDecCoin(t, 1),
	}

	require.Equal(t, order.orderID.GroupID(), ev.ID.GroupID())

	err := scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// Wait for this to complete. The lease has been created so it
	// stops on it own
	<-order.lc.Done()

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should not have called unreserve
	scaffold.cluster.AssertNotCalled(t, "Unreserve", mock.Anything, mock.Anything)
}

func Test_BidOrderAndThenLeaseCreatedForDifferentDeployment(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	// Wait for the first broadcast
	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mvbeta.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.ID.OrderID(), scaffold.orderID)

	otherOrderID := scaffold.orderID
	otherOrderID.GSeq++
	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(otherOrderID, scaffold.testAddr))

	ev := &mtypes.EventLeaseCreated{
		ID:    leaseID,
		Price: testutil.AkashDecCoin(t, 1),
	}

	subscriber, err := scaffold.testBus.Subscribe()
	require.NoError(t, err)
	err = scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// Wait for the event to be published
	testutil.ChannelWaitForValue(t, subscriber.Events())

	// Should not have called unreserve yet
	scaffold.cluster.AssertNotCalled(t, "Unreserve", mock.Anything, mock.Anything)

	// Shutdown after the message has been published
	order.lc.Shutdown(nil)

	// Should have called unreserve
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)

	// The last call should be a broadcast to close the bid
	txCalls := scaffold.txClient.Calls
	require.NotEqual(t, 0, len(txCalls))
	lastBroadcast := txCalls[len(txCalls)-1]
	require.Equal(t, "BroadcastMsgs", lastBroadcast.Method)

	closeBidMsg := requireMsgType[*mvbeta.MsgCloseBid](t, lastBroadcast.Arguments[1])

	expectedBidID := mtypes.MakeBidID(order.orderID, scaffold.testAddr)
	require.Equal(t, closeBidMsg.ID, expectedBidID)
}

func Test_ShouldNotBidWhenAlreadySet(t *testing.T) {
	order, scaffold, reservationFulfilledNotify := makeOrderForTest(t, true, mvbeta.BidOpen, nil, nil, testBidCreatedAt)

	// Wait for a reserve call
	testutil.ChannelWaitForValue(t, scaffold.reserveCallNotify)

	// Should have queried for the bid
	scaffold.marketMocks.AssertCalled(t, "Bid", mock.Anything, &mvbeta.QueryBidRequest{ID: *scaffold.bidID})

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Wait for the reservation to be processed
	testutil.ChannelWaitForValue(t, reservationFulfilledNotify)

	// Close the order
	ev := &mtypes.EventOrderClosed{
		ID: scaffold.orderID,
	}

	err := scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// Wait for it to stop
	<-order.lc.Done()

	// Should have called unreserve during shutdown
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)

	var broadcast []sdk.Msg
	select {
	case broadcast = <-scaffold.broadcasts:
	default:
	}

	closeBid := requireMsgType[*mvbeta.MsgCloseBid](t, broadcast)

	require.Equal(t, closeBid.ID, *scaffold.bidID)
}

func Test_ShouldCloseBidWhenAlreadySetAndOld(t *testing.T) {
	pricing, err := MakeRandomRangePricing()
	require.NoError(t, err)
	cfg := Config{
		PricingStrategy: pricing,
		Deposit:         sdk.NewInt64Coin(testutil.CoinDenom, 1),
		BidTimeout:      time.Second,
		Attributes:      nil,
	}

	order, scaffold, _ := makeOrderForTest(t, true, mvbeta.BidOpen, nil, &cfg, 1)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should not have called reserve
	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should have closed the bid
	expMsgs := []sdk.Msg{&mvbeta.MsgCloseBid{
		ID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
	}}

	scaffold.txClient.AssertCalled(t, "BroadcastMsgs", mock.Anything, expMsgs, mock.Anything)
}

func Test_ShouldExitWhenAlreadySetAndLost(t *testing.T) {
	pricing, err := MakeRandomRangePricing()
	require.NoError(t, err)
	cfg := Config{
		PricingStrategy: pricing,
		Deposit:         sdk.NewInt64Coin(testutil.CoinDenom, 1),
		BidTimeout:      time.Minute,
		Attributes:      nil,
	}

	order, scaffold, _ := makeOrderForTest(t, true, mvbeta.BidLost, nil, &cfg, testBidCreatedAt)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should not have called reserve
	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should not have closed the bid
	expMsgs := &mvbeta.MsgCloseBid{
		ID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
	}

	scaffold.txClient.AssertNotCalled(t, "BroadcastMsgs", mock.Anything, expMsgs, mock.Anything)
}

func Test_ShouldCloseBidWhenAlreadySetAndThenTimeout(t *testing.T) {
	pricing, err := MakeRandomRangePricing()
	require.NoError(t, err)
	cfg := Config{
		PricingStrategy: pricing,
		Deposit:         sdk.NewInt64Coin(testutil.CoinDenom, 1),
		BidTimeout:      6 * time.Second,
		Attributes:      nil,
	}

	order, scaffold, _ := makeOrderForTest(t, true, mvbeta.BidOpen, nil, &cfg, testBidCreatedAt)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should have called reserve
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should have closed the bid
	expMsgs := []sdk.Msg{
		&mvbeta.MsgCloseBid{
			ID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
		},
	}
	scaffold.txClient.AssertCalled(t, "BroadcastMsgs", mock.Anything, expMsgs, mock.Anything)

	// Should have called unreserve
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID)
}

func Test_ShouldRecognizeLeaseCreatedIfBiddingIsSkipped(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, true, mvbeta.BidOpen, nil, nil, testBidCreatedAt)

	// Wait for a reserve call
	testutil.ChannelWaitForValue(t, scaffold.reserveCallNotify)

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should not have called unreserve
	scaffold.cluster.AssertNotCalled(t, "Unreserve", mock.Anything, mock.Anything)

	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(scaffold.orderID, scaffold.testAddr))

	ev := &mtypes.EventLeaseCreated{
		ID:    leaseID,
		Price: testutil.AkashDecCoin(t, 1),
	}

	err := scaffold.testBus.Publish(ev)
	require.NoError(t, err)

	// Wait for it to stop
	<-order.lc.Done()

	// Should not have called unreserve during shutdown
	scaffold.cluster.AssertNotCalled(t, "Unreserve", mock.Anything, mock.Anything)

	var broadcast []sdk.Msg

	select {
	case broadcast = <-scaffold.broadcasts:
	default:
	}
	// Should never have broadcast
	require.Nil(t, broadcast)
}

func (tbps testBidPricingStrategy) CalculatePrice(_ context.Context, _ Request) (sdk.DecCoin, error) {
	return sdk.NewInt64DecCoin(testutil.CoinDenom, int64(tbps)), nil
}

func Test_BidOrderUsesBidPricingStrategy(t *testing.T) {
	expectedBid := int64(37)
	// Create a test strategy that gives a fixed price
	pricing := testBidPricingStrategy(expectedBid)
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, pricing, nil, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	createBidMsg := requireMsgType[*mvbeta.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.ID.OrderID(), scaffold.orderID)

	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.Equal(t, priceAmount, sdkmath.LegacyNewDec(expectedBid))

	// After the broadcast call shut the thing down
	// and then check what was broadcast
	order.lc.Shutdown(nil)

	// Should have called unreserve once, nothing happened after the bid
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func (afbps alwaysFailsBidPricingStrategy) CalculatePrice(_ context.Context, _ Request) (sdk.DecCoin, error) {
	return sdk.DecCoin{}, afbps.failure
}

var errBidPricingAlwaysFails = errors.New("bid pricing fail in test")

func Test_BidOrderFailsAndAborts(t *testing.T) {
	// Create a test strategy that gives a fixed price
	pricing := alwaysFailsBidPricingStrategy{failure: errBidPricingAlwaysFails}
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, pricing, nil, testBidCreatedAt)

	<-order.lc.Done() // Stops whenever the bid pricing is called and returns an errro

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	var broadcast []sdk.Msg

	select {
	case broadcast = <-scaffold.broadcasts:
	default:
	}
	// Should never have broadcast since bid pricing failed
	require.Nil(t, broadcast)

	// Should have called unreserve once, nothing happened after the bid
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

func Test_ShouldntBidIfOrderAttrsDontMatch(t *testing.T) {
	// Create a config that only bids on orders with given attributes
	cfg := &Config{Attributes: attrtypes.Attributes{
		{
			Key:   "owner",
			Value: "me",
		},
	}}
	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, cfg, testBidCreatedAt)

	<-order.lc.Done() // Stops whenever it figures it shouldn't bid

	// Should not have called reserve ever
	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	var broadcast []sdk.Msg

	select {
	case broadcast = <-scaffold.broadcasts:
	default:
	}
	// Should never have broadcast since bid was declined
	require.Nil(t, broadcast)

	// Should not have called unreserve ever, as nothing was ever reserved
	scaffold.cluster.AssertNotCalled(t, "Unreserve", scaffold.orderID, mock.Anything)
}

// TODO - add test failing the call to Broadcast on TxClient and
// and then confirm that the reservation is cancelled
