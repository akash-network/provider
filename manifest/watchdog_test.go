package manifest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	clientmocks "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	types "github.com/akash-network/akash-api/go/node/market/v1beta4"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"

	"github.com/akash-network/node/testutil"

	"github.com/akash-network/provider/session"
)

type watchdogTestScaffold struct {
	client     *clientmocks.Client
	parentCh   chan struct{}
	doneCh     chan dtypes.DeploymentID
	broadcasts chan []sdk.Msg
	leaseID    types.LeaseID
	provider   ptypes.Provider
}

func makeWatchdogTestScaffold(t *testing.T, timeout time.Duration) (*watchdog, *watchdogTestScaffold) {
	scaffold := &watchdogTestScaffold{}
	scaffold.parentCh = make(chan struct{})
	scaffold.doneCh = make(chan dtypes.DeploymentID, 1)
	scaffold.provider = testutil.Provider(t)
	scaffold.leaseID = testutil.LeaseID(t)
	scaffold.leaseID.Provider = scaffold.provider.Owner
	scaffold.broadcasts = make(chan []sdk.Msg, 1)

	txClientMock := &clientmocks.TxClient{}
	txClientMock.On("Broadcast", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		scaffold.broadcasts <- args.Get(1).([]sdk.Msg)
	}).Return(&sdk.Result{}, nil)

	scaffold.client = &clientmocks.Client{}
	scaffold.client.On("Tx").Return(txClientMock)
	sess := session.New(testutil.Logger(t), scaffold.client, &scaffold.provider, -1)

	require.NotNil(t, sess.Client())

	wd := newWatchdog(sess, scaffold.parentCh, scaffold.doneCh, scaffold.leaseID, timeout)

	return wd, scaffold
}

func TestWatchdogTimeout(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 3*time.Second)

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was sent
	broadcasts := testutil.ChannelWaitForValue(t, scaffold.broadcasts)
	require.IsType(t, []sdk.Msg{}, broadcasts)

	msgs := broadcasts.([]sdk.Msg)
	require.Len(t, msgs, 1)
	require.IsType(t, &types.MsgCloseBid{}, msgs[0])

	msg := msgs[0].(*types.MsgCloseBid)
	require.Equal(t, scaffold.leaseID, msg.BidID.LeaseID())

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}

func TestWatchdogStops(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 1*time.Minute)

	wd.stop() // ask it to stop immediately
	wd.stop() // ask it to stop a second time, this is expected usage

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was not sent
	select {
	case <-scaffold.broadcasts:
		t.Fatal("should no have broadcast any message")
	default:
	}

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}

func TestWatchdogStopsOnParent(t *testing.T) {
	wd, scaffold := makeWatchdogTestScaffold(t, 1*time.Minute)

	close(scaffold.parentCh) // ask it to stop immediately

	select {
	case <-wd.lc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting on watchdog to stop")
	}

	// Check that close bid was not sent
	select {
	case <-scaffold.broadcasts:
		t.Fatal("should no have broadcast any message")
	default:
	}

	deploymentID := testutil.ChannelWaitForValue(t, scaffold.doneCh)
	require.Equal(t, deploymentID, scaffold.leaseID.DeploymentID())
}
