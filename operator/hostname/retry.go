package hostname

import (
	"context"
	"time"

	chostname "github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
)

const (
	hostnameRetryBase = 2 * time.Second
	hostnameRetryMax  = 5 * time.Minute
)

// hostnameRetries reschedules failed add or update events per hostname with
// exponential backoff, so one failing route (a policy the gateway has not accepted
// yet, a transient API error) does not restart the whole reconcile loop. Continuing
// the loop alone is not enough: no new event may ever arrive for that hostname. A
// newer event for a hostname supersedes its pending retry.
//
// Only the timer callbacks run outside the event loop, and they only send on due;
// every other method must be called from the loop.
type hostnameRetries struct {
	ctx     context.Context
	base    time.Duration
	max     time.Duration
	seq     uint64
	pending map[string]*pendingRetry
	due     chan retryToken
}

type pendingRetry struct {
	ev       chostname.ResourceEvent
	attempts int
	seq      uint64
	timer    *time.Timer
}

// retryToken identifies one scheduled attempt. A token whose seq no longer matches
// the hostname's pending retry was superseded and is dropped.
type retryToken struct {
	hostname string
	seq      uint64
}

func newHostnameRetries(ctx context.Context, base, maxDelay time.Duration) *hostnameRetries {
	return &hostnameRetries{
		ctx:     ctx,
		base:    base,
		max:     maxDelay,
		pending: make(map[string]*pendingRetry),
		due:     make(chan retryToken, 64),
	}
}

// schedule queues ev for another attempt and returns the delay, which doubles with
// each consecutive failure of the hostname up to the maximum.
func (r *hostnameRetries) schedule(ev chostname.ResourceEvent) time.Duration {
	hostname := ev.GetHostname()

	p, ok := r.pending[hostname]
	if !ok {
		p = &pendingRetry{}
		r.pending[hostname] = p
	}
	if p.timer != nil {
		p.timer.Stop()
	}

	p.attempts++
	r.seq++
	p.seq = r.seq
	p.ev = ev

	delay := r.backoff(p.attempts)
	tok := retryToken{hostname: hostname, seq: p.seq}
	p.timer = time.AfterFunc(delay, func() {
		select {
		case r.due <- tok:
		case <-r.ctx.Done():
		}
	})

	return delay
}

func (r *hostnameRetries) backoff(attempts int) time.Duration {
	delay := r.base << min(attempts-1, 20)
	if delay <= 0 || delay > r.max {
		return r.max
	}
	return delay
}

// take returns the event a due token refers to, or false when the retry was
// superseded or forgotten. The failure count is kept, so a further failure backs
// off longer.
func (r *hostnameRetries) take(tok retryToken) (chostname.ResourceEvent, bool) {
	p, ok := r.pending[tok.hostname]
	if !ok || p.seq != tok.seq {
		return nil, false
	}
	p.timer = nil
	return p.ev, true
}

// forget drops the hostname's pending retry and failure count, after a success or
// when a newer event arrives.
func (r *hostnameRetries) forget(hostname string) {
	if p, ok := r.pending[hostname]; ok {
		if p.timer != nil {
			p.timer.Stop()
		}
		delete(r.pending, hostname)
	}
}

// stop cancels every pending retry.
func (r *hostnameRetries) stop() {
	for hostname := range r.pending {
		r.forget(hostname)
	}
}
