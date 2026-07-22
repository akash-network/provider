package bidengine

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	metricsutils "pkg.akt.dev/go/util/metrics"

	atestutil "pkg.akt.dev/go/testutil"
)

// Test_ReservationCounterIncrements guards that the reservation metrics are
// actually recorded. The success/fail counters were calling WithLabelValues
// without a terminal .Inc(), making them silent no-ops, so a full reserve +
// unreserve cycle never moved them.
func Test_ReservationCounterIncrements(t *testing.T) {
	openSuccess := testutil.ToFloat64(
		reservationCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.SuccessLabel))
	closeSuccess := testutil.ToFloat64(
		reservationCounter.WithLabelValues("close", metricsutils.SuccessLabel))

	order, scaffold, _ := makeOrderForTest(t, false, mvbeta.BidStateInvalid, nil, nil, testBidCreatedAt)

	// Wait for the bid to be broadcast — by then the reservation has succeeded.
	_ = atestutil.ChannelWaitForValue(t, scaffold.broadcasts)
	scaffold.cluster.AssertCalled(t, "Reserve", scaffold.orderID, mock.Anything)

	// Shutting down triggers the unreserve path.
	order.lc.Shutdown(nil)
	scaffold.cluster.AssertCalled(t, "Unreserve", scaffold.orderID, mock.Anything)

	gotOpen := testutil.ToFloat64(
		reservationCounter.WithLabelValues(metricsutils.OpenLabel, metricsutils.SuccessLabel))
	gotClose := testutil.ToFloat64(
		reservationCounter.WithLabelValues("close", metricsutils.SuccessLabel))

	require.Equal(t, openSuccess+1, gotOpen, "reservation open-success counter must increment")
	require.Equal(t, closeSuccess+1, gotClose, "reservation close-success counter must increment")
}
