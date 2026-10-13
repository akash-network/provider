package hostname

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	"github.com/akash-network/provider/operator/common"
)

// Retain the lease as well as the hostname: a hostname can move while its old
// route is incomplete, and that old route still needs cleanup in its namespace.
type hostnameWorkKey struct {
	hostname string
	lease    mtypes.LeaseID
}

type pendingHostname struct {
	event       chostname.ResourceEvent
	attempts    uint
	nextAttempt time.Time
	lastError   string
}

func (op *hostnameOperator) refreshHostnames(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot, _, err := op.listHostnameState(ctx)
	if err != nil {
		return err
	}
	for _, ev := range snapshot {
		op.queueHostname(ev)
	}
	// Also revisit actual routes, so a missed deletion is repaired. Every
	// attempt fetches current desired state before changing a route.
	for hostname, entry := range op.hostnames {
		op.queueHostname(hostnameResourceEvent{
			eventType: ctypes.ProviderResourceUpdate, hostname: hostname,
			leaseID: entry.presentLease, serviceName: entry.presentServiceName,
			externalPort: entry.presentExternalPort,
		})
	}
	return nil
}

func (op *hostnameOperator) queueHostname(ev chostname.ResourceEvent) {
	if op.pending == nil {
		op.pending = make(map[hostnameWorkKey]pendingHostname)
	}
	key := hostnameWorkKey{hostname: ev.GetHostname(), lease: ev.GetLeaseID()}
	work, exists := op.pending[key]
	work.event = ev
	// Relists must not move an older item behind all the newly discovered work,
	// or reset its backoff. With API throttling that would starve the tail of
	// every snapshot. Deletes are urgent and supersede a delayed failed create.
	if !exists || ev.GetEventType() == ctypes.ProviderResourceDelete {
		work.nextAttempt = time.Now()
	}
	op.pending[key] = work
	op.flagPendingData()
}

func (op *hostnameOperator) nextHostnameRetry() (time.Duration, bool) {
	var next time.Time
	for _, work := range op.pending {
		if next.IsZero() || work.nextAttempt.Before(next) {
			next = work.nextAttempt
		}
	}
	return max(0, time.Until(next)), !next.IsZero()
}

// retryHostname processes one due item so event ingestion, deletions and web
// status preparation remain responsive even when many hostnames are pending.
// Only the observation goroutine owns pending and hostnames.
func (op *hostnameOperator) retryHostname(ctx context.Context) {
	now := time.Now()
	var key hostnameWorkKey
	var work pendingHostname
	for candidate, pending := range op.pending {
		if !pending.nextAttempt.After(now) && (work.event == nil || pending.nextAttempt.Before(work.nextAttempt)) {
			key, work = candidate, pending
		}
	}
	if work.event == nil {
		return
	}
	// Bound network requests as well as extension acceptance. One unavailable API
	// must not hold the observation goroutine indefinitely.
	attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := op.reconcileHostname(attemptCtx, work.event)
	cancel()
	if err == nil {
		delete(op.pending, key)
		op.flagPendingData()
		return
	}
	if ctx.Err() != nil {
		return
	}
	work.attempts++
	delay := hostnameRetryDelay(op.cfg.RetryDelay, work.attempts)
	if errors.Is(err, gateway.ErrRouteExtensionPending) {
		// Acceptance is asynchronous, not a failed deployment. Probe again soon
		// without occupying the actor while the gateway observes the filter.
		delay = min(delay, 250*time.Millisecond)
		op.log.Debug("waiting for hostname route", "hostname", key.hostname, "lease", key.lease.String(), "err", err)
	} else {
		op.log.Error("hostname reconciliation failed; will retry", "hostname", key.hostname,
			"lease", key.lease.String(), "err", err, "retry-in", delay)
	}
	work.lastError = err.Error()
	work.nextAttempt = time.Now().Add(delay)
	op.pending[key] = work
	op.flagPendingData()
}

func hostnameRetryDelay(base time.Duration, attempts uint) time.Duration {
	const maximum = 30 * time.Second
	delay := min(max(base, time.Millisecond), maximum)
	for i := uint(1); i < attempts && delay < maximum; i++ {
		delay = min(delay*2, maximum)
	}
	return delay
}

func (op *hostnameOperator) reconcileHostname(ctx context.Context, ev chostname.ResourceEvent) error {
	// Events are hints. Fetch current desired state on every attempt so an old
	// replay or a delayed retry cannot resurrect a deleted or reassigned route.
	ph, err := op.ac.AkashV2beta2().ProviderHosts(op.ns).Get(ctx, ev.GetHostname(), metav1.GetOptions{})
	if kerrors.IsNotFound(err) {
		return op.applyDeleteEvent(ctx, ev)
	}
	if err != nil {
		return err
	}
	current, err := hostnameEventFromProviderHost(ph, ctypes.ProviderResourceUpdate)
	if err != nil {
		return err
	}
	if !current.GetLeaseID().Equals(ev.GetLeaseID()) {
		key := hostnameWorkKey{hostname: current.GetHostname(), lease: current.GetLeaseID()}
		if _, pending := op.pending[key]; !pending {
			op.queueHostname(current)
		}
		return op.applyDeleteEvent(ctx, ev)
	}
	return op.applyAddOrUpdateEvent(ctx, current)
}

func (op *hostnameOperator) preparePendingData(pd common.PreparedResult) error {
	type pendingStatus struct {
		Hostname    string    `json:"hostname"`
		Lease       string    `json:"lease"`
		Namespace   string    `json:"namespace"`
		Attempts    uint      `json:"attempts"`
		NextAttempt time.Time `json:"next-attempt"`
		LastError   string    `json:"last-error"`
	}
	entries := make([]pendingStatus, 0, len(op.pending))
	for key, work := range op.pending {
		entries = append(entries, pendingStatus{
			Hostname: key.hostname, Lease: key.lease.String(), Namespace: builder.LidNS(key.lease),
			Attempts: work.attempts, NextAttempt: work.nextAttempt, LastError: work.lastError,
		})
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(entries); err != nil {
		return err
	}
	pd.Set(buf.Bytes())
	return nil
}
