package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	cmtlog "github.com/cometbft/cometbft/libs/log"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"
	rpcserver "github.com/cometbft/cometbft/rpc/jsonrpc/server"
	rpctypes "github.com/cometbft/cometbft/rpc/jsonrpc/types"
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

// newQueryOnlyNode serves the CometBFT JSON-RPC status route the chain event service
// (pkg.akt.dev/go/util/events) starts on, but no websocket endpoint, so every block
// subscription it dials fails.
func newQueryOnlyNode(t *testing.T) *httptest.Server {
	t.Helper()

	routes := map[string]*rpcserver.RPCFunc{
		"status": rpcserver.NewRPCFunc(func(*rpctypes.Context) (*ctypes.ResultStatus, error) {
			return &ctypes.ResultStatus{SyncInfo: ctypes.SyncInfo{LatestBlockHeight: 1}}, nil
		}, ""),
	}

	mux := http.NewServeMux()
	rpcserver.RegisterRPCFuncs(mux, routes, cmtlog.NewNopLogger())

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// The chain event service logs under module "events", so a filter enabling its warnings
// applies to them: here the warning on a block subscription the node does not serve.
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

	node := newQueryOnlyNode(t)

	// The service starts although the node serves no websocket: it redials in the
	// background rather than failing the provider's startup.
	svc, err := newEventsService(ctx, node.URL, bus, root.With("module", "provider"))
	require.NoError(t, err, "a node without a websocket must not fail startup")
	defer svc.Shutdown()

	var entry map[string]any
	require.Eventually(t, func() bool {
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.Contains(line, "block subscription dial failed") {
				return json.Unmarshal([]byte(line), &entry) == nil
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "failed subscription warning not logged under events:warn,*:error")

	require.Equal(t, "events", entry["module"])
	require.Equal(t, "warn", entry["level"], "only a warning tests the events:warn entry: an error passes *:error anyway")
}

func TestRunCmdDoesNotExposeMonitorHealthcheckFlags(t *testing.T) {
	cmd := RunCmd()
	require.Nil(t, cmd.Flags().Lookup("monitor-healthcheck-period"))
	require.Nil(t, cmd.Flags().Lookup("monitor-healthcheck-period-jitter"))
}

// A withdrawal period of 0 turns withdrawals off. A negative one is a mistake,
// not another way to turn them off, and a period shorter than the funds check
// interval cannot be kept.
func TestValidateWithdrawalPeriod(t *testing.T) {
	const fundsCheckInterval = 10 * time.Minute
	for _, tc := range []struct {
		name    string
		period  time.Duration
		wantErr bool
	}{
		{name: "withdrawals off", period: 0},
		{name: "default", period: 24 * time.Hour},
		{name: "funds check interval", period: fundsCheckInterval},
		{name: "shorter than the funds check interval", period: fundsCheckInterval - time.Minute, wantErr: true},
		{name: "negative", period: -24 * time.Hour, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWithdrawalPeriod(tc.period, fundsCheckInterval)
			if tc.wantErr {
				require.ErrorContains(t, err, FlagWithdrawalPeriod)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
