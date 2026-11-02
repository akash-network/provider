package hostname

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

func retryEvent(hostname string) hostnameResourceEvent {
	return hostnameResourceEvent{eventType: ctypes.ProviderResourceAdd, hostname: hostname}
}

func nextDue(t *testing.T, r *hostnameRetries) retryToken {
	t.Helper()
	select {
	case tok := <-r.due:
		return tok
	case <-time.After(2 * time.Second):
		t.Fatal("retry never came due")
		return retryToken{}
	}
}

// TestHostnameRetriesDelivers asserts a scheduled retry comes due and returns its
// event.
func TestHostnameRetriesDelivers(t *testing.T) {
	r := newHostnameRetries(context.Background(), time.Millisecond, time.Second)
	defer r.stop()

	r.schedule(retryEvent("a.example.com"))
	ev, ok := r.take(nextDue(t, r))
	require.True(t, ok)
	require.Equal(t, "a.example.com", ev.GetHostname())
}

// TestHostnameRetriesSuperseded asserts a retry rescheduled or forgotten before it
// fires is dropped, so a stale event never overwrites newer state.
func TestHostnameRetriesSuperseded(t *testing.T) {
	r := newHostnameRetries(context.Background(), time.Millisecond, time.Second)
	defer r.stop()

	stale := retryToken{hostname: "a.example.com", seq: 1}
	r.schedule(retryEvent("a.example.com"))
	r.schedule(retryEvent("a.example.com"))
	_, ok := r.take(stale)
	require.False(t, ok, "superseded by the second schedule")

	r.forget("a.example.com")
	_, ok = r.take(retryToken{hostname: "a.example.com", seq: 2})
	require.False(t, ok, "forgotten")
}

// TestHostnameRetriesBackoff asserts the delay doubles per consecutive failure up
// to the maximum, and resets once forgotten.
func TestHostnameRetriesBackoff(t *testing.T) {
	r := newHostnameRetries(context.Background(), time.Hour, 3*time.Hour)
	defer r.stop()

	ev := retryEvent("a.example.com")
	require.Equal(t, time.Hour, r.schedule(ev))
	require.Equal(t, 2*time.Hour, r.schedule(ev))
	require.Equal(t, 3*time.Hour, r.schedule(ev), "capped")
	require.Equal(t, 3*time.Hour, r.schedule(ev))

	r.forget("a.example.com")
	require.Equal(t, time.Hour, r.schedule(ev), "reset after forget")

	require.Equal(t, time.Hour, r.schedule(retryEvent("b.example.com")), "per hostname")
}
