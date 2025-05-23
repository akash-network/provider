package bidengine

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	tpubsub "github.com/troian/pubsub"

	"github.com/akash-network/provider/operator/waiter"
	"github.com/akash-network/provider/tools/fromctx"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/akash-api/go/node/types/constants"
	"github.com/akash-network/akash-api/go/sdkutil"

	"github.com/stretchr/testify/mock"

	"github.com/stretchr/testify/require"

	audittypes "github.com/akash-network/akash-api/go/node/audit/v1beta3"
	clientmocks "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	atypes "github.com/akash-network/akash-api/go/node/types/v1beta3"

	"github.com/akash-network/node/pubsub"
	"github.com/akash-network/node/testutil"

	clustermocks "github.com/akash-network/provider/cluster/mocks"
	clmocks "github.com/akash-network/provider/cluster/types/v1beta3/mocks"
	"github.com/akash-network/provider/session"
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
	txClient          *clientmocks.TxClient
	cluster           *clustermocks.Cluster
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
	groupResult := &dtypes.QueryGroupResponse{}
	groupResult.Group.GroupSpec.Name = "testGroupName"
	groupResult.Group.GroupSpec.Resources = make(dtypes.ResourceUnits, 1)

	cpu := atypes.CPU{}
	cpu.Units = atypes.NewResourceValue(uint64(dtypes.GetValidationConfig().Unit.Min.CPU))

	gpu := atypes.GPU{}
	gpu.Units = atypes.NewResourceValue(uint64(dtypes.GetValidationConfig().Unit.Min.GPU))

	memory := atypes.Memory{}
	memory.Quantity = atypes.NewResourceValue(dtypes.GetValidationConfig().Unit.Min.Memory)

	storage := atypes.Volumes{
		atypes.Storage{
			Quantity: atypes.NewResourceValue(dtypes.GetValidationConfig().Unit.Min.Storage),
		},
	}

	clusterResources := atypes.Resources{
		ID:      1,
		CPU:     &cpu,
		GPU:     &gpu,
		Memory:  &memory,
		Storage: storage,
	}
	price := sdk.NewInt64DecCoin(testutil.CoinDenom, 23)
	resource := dtypes.ResourceUnit{
		Resources: clusterResources,
		Count:     2,
		Price:     price,
	}

	groupResult.Group.GroupSpec.Resources[0] = resource

	homeDir, _ := os.MkdirTemp("", "akash-network-test-*")

	queryClientMock := &clientmocks.QueryClient{}
	queryClientMock.On("Group", mock.Anything, mock.Anything).Return(groupResult, nil)
	queryClientMock.On("Orders", mock.Anything, mock.Anything).Return(&mtypes.QueryOrdersResponse{}, nil)
	queryClientMock.On("Provider", mock.Anything, mock.Anything).Return(&ptypes.QueryProviderResponse{}, nil)
	queryClientMock.On("ClientContext").Return(sdkclient.Context{HomeDir: homeDir}, nil)

	txMocks := &clientmocks.TxClient{}
	s.broadcasts = make(chan []sdk.Msg, 1)

	txMocks.On("Broadcast", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		s.broadcasts <- args.Get(1).([]sdk.Msg)
	}).Return(&sdk.Result{}, nil)

	clientMocks := &clientmocks.Client{}
	clientMocks.On("Query").Return(queryClientMock)
	clientMocks.On("Tx").Return(txMocks)

	s.client = clientMocks
	s.queryClient = queryClientMock
	s.txClient = txMocks

	mockReservation := &clmocks.Reservation{}
	mockReservation.On("OrderID").Return(s.orderID)
	mockReservation.On("Resources").Return(groupResult.Group)
	mockReservation.On("GetAllocatedResources").Return(groupResult.Group.GroupSpec.Resources)

	s.cluster = &clustermocks.Cluster{}
	s.reserveCallNotify = make(chan int, 1)
	s.cluster.On("Reserve", s.orderID, &(groupResult.Group)).Run(func(_ mock.Arguments) {
		s.reserveCallNotify <- 0
		time.Sleep(time.Second) // add a delay before returning response, to test race conditions
	}).Return(mockReservation, nil)

	s.cluster.On("Unreserve", s.orderID, mock.Anything).Return(nil)
}

type nullProviderAttrSignatureService struct{}

func (nullProviderAttrSignatureService) GetAuditorAttributeSignatures(_ string) ([]audittypes.Provider, error) {
	return nil, nil // Return no attributes & no error
}

func (nullProviderAttrSignatureService) GetAttributes() (atypes.Attributes, error) {
	return nil, nil // Return no attributes & no error
}

const testBidCreatedAt = 1234556789

func makeOrderForTest(
	t *testing.T,
	checkForExistingBid bool,
	bidState mtypes.Bid_State,
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
	cfg.Deposit = mtypes.DefaultBidMinDeposit
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
		queryBidRequest := &mtypes.QueryBidRequest{
			ID: bidID,
		}
		response := &mtypes.QueryBidResponse{
			Bid: mtypes.Bid{
				BidID:     bidID,
				State:     bidState,
				Price:     sdk.NewInt64DecCoin(testutil.CoinDenom, int64(testutil.RandRangeInt(100, 1000))),
				CreatedAt: testBidCreatedAt,
			},
		}
		scaffold.queryClient.On("Bid", mock.Anything, queryBidRequest).Return(response, nil)
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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, nil, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mtypes.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.Order, scaffold.orderID)

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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, &Config{
		BidTimeout: 5 * time.Second,
	}, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mtypes.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.Order, scaffold.orderID)

	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.True(t, priceAmount.GT(sdk.NewDec(0)))
	require.True(t, priceAmount.LT(sdk.NewDec(100)))

	// After the broadcast call the timeout should take effect
	// and then close the bid, unreserving capacity in the process
	broadcast = testutil.ChannelWaitForValue(t, scaffold.broadcasts)

	_ = requireMsgType[*mtypes.MsgCloseBid](t, broadcast)

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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, pricing, nil, testBidCreatedAt)

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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, nil, testBidCreatedAt)

	testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	// Should have called reserve once at this point
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	ev := mtypes.EventOrderClosed{
		Context: sdkutil.BaseModuleEvent{},
		ID:      scaffold.orderID,
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
	order, scaffold, reservationFulfilledNotify := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, nil, testBidCreatedAt)

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
	ev := mtypes.EventOrderClosed{
		Context: sdkutil.BaseModuleEvent{},
		ID:      scaffold.orderID,
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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, nil, testBidCreatedAt)

	// Wait for first broadcast
	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	createBidMsg := requireMsgType[*mtypes.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.Order, scaffold.orderID)
	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.GreaterOrEqual(t, priceAmount.TruncateInt64(), int64(1))
	require.Less(t, priceAmount.TruncateInt64(), int64(100))

	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(scaffold.orderID, scaffold.testAddr))

	ev := mtypes.EventLeaseCreated{
		Context: sdkutil.BaseModuleEvent{},
		ID:      leaseID,
		Price:   testutil.AkashDecCoin(t, 1),
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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, nil, testBidCreatedAt)

	// Wait for first broadcast
	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	createBidMsg := requireMsgType[*mtypes.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.Order, scaffold.orderID)

	otherOrderID := scaffold.orderID
	otherOrderID.GSeq++
	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(otherOrderID, scaffold.testAddr))

	ev := mtypes.EventLeaseCreated{
		Context: sdkutil.BaseModuleEvent{},
		ID:      leaseID,
		Price:   testutil.AkashDecCoin(t, 1),
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
	require.Equal(t, "Broadcast", lastBroadcast.Method)

	closeBidMsg := requireMsgType[*mtypes.MsgCloseBid](t, lastBroadcast.Arguments[1])

	expectedBidID := mtypes.MakeBidID(order.orderID, scaffold.testAddr)
	require.Equal(t, closeBidMsg.BidID, expectedBidID)
}

func Test_ShouldNotBidWhenAlreadySet(t *testing.T) {
	order, scaffold, reservationFulfilledNotify := makeOrderForTest(t, true, mtypes.BidOpen, nil, nil, testBidCreatedAt)

	// Wait for a reserve call
	testutil.ChannelWaitForValue(t, scaffold.reserveCallNotify)

	// Should have queried for the bid
	scaffold.queryClient.AssertCalled(t, "Bid", mock.Anything, &mtypes.QueryBidRequest{ID: *scaffold.bidID})

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Wait for the reservation to be processed
	testutil.ChannelWaitForValue(t, reservationFulfilledNotify)

	// Close the order
	ev := mtypes.EventOrderClosed{
		Context: sdkutil.BaseModuleEvent{},
		ID:      scaffold.orderID,
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

	closeBid := requireMsgType[*mtypes.MsgCloseBid](t, broadcast)

	require.Equal(t, closeBid.BidID, *scaffold.bidID)
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

	order, scaffold, _ := makeOrderForTest(t, true, mtypes.BidOpen, nil, &cfg, 1)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should not have called reserve
	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should have closed the bid
	expMsgs := []sdk.Msg{&mtypes.MsgCloseBid{
		BidID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
	}}

	scaffold.txClient.AssertCalled(t, "Broadcast", mock.Anything, expMsgs, mock.Anything)
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

	order, scaffold, _ := makeOrderForTest(t, true, mtypes.BidLost, nil, &cfg, testBidCreatedAt)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should not have called reserve
	scaffold.cluster.AssertNotCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should not have closed the bid
	expMsgs := &mtypes.MsgCloseBid{
		BidID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
	}

	scaffold.txClient.AssertNotCalled(t, "Broadcast", mock.Anything, expMsgs, mock.Anything)
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

	order, scaffold, _ := makeOrderForTest(t, true, mtypes.BidOpen, nil, &cfg, testBidCreatedAt)

	testutil.ChannelWaitForClose(t, order.lc.Done())

	// Should have called reserve
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should have closed the bid
	expMsgs := []sdk.Msg{
		&mtypes.MsgCloseBid{
			BidID: mtypes.MakeBidID(order.orderID, scaffold.testAddr),
		},
	}
	scaffold.txClient.AssertCalled(t, "Broadcast", mock.Anything, expMsgs, mock.Anything)

	// Should have called unreserve
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID)
}

func Test_ShouldRecognizeLeaseCreatedIfBiddingIsSkipped(t *testing.T) {
	order, scaffold, _ := makeOrderForTest(t, true, mtypes.BidOpen, nil, nil, testBidCreatedAt)

	// Wait for a reserve call
	testutil.ChannelWaitForValue(t, scaffold.reserveCallNotify)

	// Should have called reserve once
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Should not have called unreserve
	scaffold.cluster.AssertNotCalled(t, "Unreserve", mock.Anything, mock.Anything)

	leaseID := mtypes.MakeLeaseID(mtypes.MakeBidID(scaffold.orderID, scaffold.testAddr))

	ev := mtypes.EventLeaseCreated{
		Context: sdkutil.BaseModuleEvent{},
		ID:      leaseID,
		Price:   testutil.AkashDecCoin(t, 1),
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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, pricing, nil, testBidCreatedAt)

	broadcast := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	createBidMsg := requireMsgType[*mtypes.MsgCreateBid](t, broadcast)

	require.Equal(t, createBidMsg.Order, scaffold.orderID)

	priceDenom := createBidMsg.Price.Denom
	require.Equal(t, testutil.CoinDenom, priceDenom)
	priceAmount := createBidMsg.Price.Amount

	require.Equal(t, priceAmount, sdk.NewDec(expectedBid))

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
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, pricing, nil, testBidCreatedAt)

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
	cfg := &Config{Attributes: atypes.Attributes{
		{
			Key:   "owner",
			Value: "me",
		},
	}}
	order, scaffold, _ := makeOrderForTest(t, false, mtypes.BidStateInvalid, nil, cfg, testBidCreatedAt)

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
