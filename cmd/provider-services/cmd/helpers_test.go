package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/util/ctxlog"
	"pkg.akt.dev/go/util/pubsub"

	"github.com/akash-network/provider/tools/logging"
)

var expectedErrMsgForRPC = "^error communicating with RPC.+$"

func TestUnwrappingRPCJSONError(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{foo:bar}") // some invalid json
	dec := json.NewDecoder(buf)
	var x interface{}
	err := dec.Decode(&x)
	require.Error(t, err)
	require.IsType(t, &json.SyntaxError{}, err)

	wrappedErr := fmt.Errorf("%w: test error", err)
	err = markRPCServerError(wrappedErr)
	require.Error(t, err)
	require.Regexp(t, expectedErrMsgForRPC, err)
}

func TestUnwrappingURLError(t *testing.T) {
	urlErr := &url.Error{
		Op:  "GET",
		URL: "a",
		Err: errors.New("test error thing"),
	}
	require.Error(t, urlErr)

	wrappedErr := fmt.Errorf("%w: test error", urlErr)
	err := markRPCServerError(wrappedErr)
	require.Error(t, err)
	require.Regexp(t, expectedErrMsgForRPC, err)
}

// syncBuffer is a writer the test reads while the event service writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// eventsNode implements the CometRPC and EventsClient methods the chain event service
// (pkg.akt.dev/go/util/events) uses to start and to watch block headers.
type eventsNode struct {
	sdkclient.CometRPC

	headers chan ctypes.ResultEvent
}

func (n *eventsNode) Status(context.Context) (*ctypes.ResultStatus, error) {
	return &ctypes.ResultStatus{SyncInfo: ctypes.SyncInfo{LatestBlockHeight: 1}}, nil
}

func (n *eventsNode) Subscribe(context.Context, string, string, ...int) (<-chan ctypes.ResultEvent, error) {
	return n.headers, nil
}

func (n *eventsNode) Unsubscribe(context.Context, string, string) error { return nil }

func (n *eventsNode) UnsubscribeAll(context.Context, string) error { return nil }

// The chain event service logs under module "events", so a filter enabling its warnings
// applies to them: here the warning on a closed block subscription.
func TestEventsServiceLogsUnderEventsModule(t *testing.T) {
	cfg, err := logging.ParseConfig("events:warn,*:error", logging.FormatJSON, false, logging.TimestampOff, false)
	require.NoError(t, err)

	out := &syncBuffer{}
	root := logging.New(out, cfg)

	// as in doRunCmd: the context carries the root logger, the run logger has module "provider"
	ctx, cancel := context.WithCancel(ctxlog.WithLogger(context.Background(), root))
	defer cancel()

	bus := pubsub.NewBus()
	defer bus.Close()

	node := &eventsNode{headers: make(chan ctypes.ResultEvent)}

	svc, err := newEventsService(ctx, node, bus, root.With("module", "provider"))
	require.NoError(t, err)
	defer svc.Shutdown()

	close(node.headers)

	var entry map[string]any
	require.Eventually(t, func() bool {
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.Contains(line, "block subscription channel closed") {
				return json.Unmarshal([]byte(line), &entry) == nil
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "closed subscription warning not logged under events:warn,*:error")

	require.Equal(t, "events", entry["module"])
}

func TestRunCmdDoesNotExposeMonitorHealthcheckFlags(t *testing.T) {
	cmd := RunCmd()
	require.Nil(t, cmd.Flags().Lookup("monitor-healthcheck-period"))
	require.Nil(t, cmd.Flags().Lookup("monitor-healthcheck-period-jitter"))
}
