package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/testutil"

	cip "github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
	cipmocks "github.com/akash-network/provider/mocks/cluster/types/clients/ip"
)

// Test_runCheck_IPConfirmContinuesAfterError guards against a per-item IP status
// error aborting the whole confirmation pass. The error is explicitly non-fatal
// ("The other results retrieved in this code are still valid"), so a failure on
// one reservation must not stop the remaining ones from being confirmed —
// otherwise their IPs stay pending forever and the IP quota leaks.
func Test_runCheck_IPConfirmContinuesAfterError(t *testing.T) {
	mkReservation := func(t *testing.T) *reservation {
		return &reservation{
			order:            testutil.OrderID(t),
			allocated:        true,
			ipsConfirmed:     false,
			endpointQuantity: 1,
		}
	}

	failing := mkReservation(t)
	ok1 := mkReservation(t)
	ok2 := mkReservation(t)

	ipClient := &cipmocks.Client{}
	ipClient.On("GetIPAddressUsage", mock.Anything).Return(cip.AddressUsage{}, nil)

	// The first reservation errors; the others return exactly the expected count.
	ipClient.On("GetIPAddressStatus", mock.Anything, failing.OrderID()).
		Return([]cip.LeaseIPStatus(nil), errors.New("transient ip operator error"))
	for _, r := range []*reservation{ok1, ok2} {
		ipClient.On("GetIPAddressStatus", mock.Anything, r.OrderID()).
			Return([]cip.LeaseIPStatus{{}}, nil)
	}

	is := &inventoryService{log: testutil.Logger(t)}
	is.clients.ip = ipClient

	state := &inventoryServiceState{
		reservations: []*reservation{failing, ok1, ok2},
	}

	res := <-is.runCheck(context.Background(), state)
	require.NoError(t, res.Error())

	out := res.Value().(runCheckResult)

	confirmed := map[string]bool{}
	for _, oid := range out.confirmedResult {
		confirmed[oid.String()] = true
	}

	require.True(t, confirmed[ok1.OrderID().String()], "reservation after the failing one must still be confirmed")
	require.True(t, confirmed[ok2.OrderID().String()], "all reservations after the failing one must still be confirmed")
}
