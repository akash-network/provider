package poster

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math/rand"
	"strings"
	"time"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	verificationv1 "pkg.akt.dev/go/node/verification/v1"

	aepinventory "github.com/akash-network/provider/verification/inventory"
)

var (
	ErrProviderSnapshotNotFound = errors.New("provider snapshot record not found")

	errMissingSnapshotter       = errors.New("missing inventory snapshotter")
	errMissingSnapshot          = errors.New("missing inventory snapshot")
	errMissingSnapshotPayload   = errors.New("missing inventory snapshot payload")
	errMissingSnapshotProvider  = errors.New("missing inventory snapshot provider")
	errMissingSnapshotHash      = errors.New("missing inventory snapshot hash")
	errMissingSnapshotTimestamp = errors.New("missing inventory snapshot timestamp")
	errUnexpectedSnapshotNonce  = errors.New("unexpected nonce in on-chain snapshot hash payload")
	errMissingQueryClient       = errors.New("missing verification query client")
	errMissingBroadcaster       = errors.New("missing snapshot hash broadcaster")
	errPosterAlreadyRunning     = errors.New("snapshot hash poster already running")
	errInvalidRunInterval       = errors.New("invalid snapshot hash poster run interval")
	errInvalidRetryDelay        = errors.New("invalid snapshot hash poster retry delay")
	errInvalidRunJitter         = errors.New("invalid snapshot hash poster run jitter")
	errInvalidRetryJitter       = errors.New("invalid snapshot hash poster retry jitter")
)

type DecisionReason string

const (
	DecisionReasonNone              DecisionReason = ""
	DecisionReasonMissingRecord     DecisionReason = "missing-record"
	DecisionReasonSuspended         DecisionReason = "suspended"
	DecisionReasonHashChanged       DecisionReason = "hash-changed"
	DecisionReasonMissingDeadline   DecisionReason = "missing-deadline"
	DecisionReasonDeadlineDue       DecisionReason = "deadline-due"
	DecisionReasonDeadlineUnchanged DecisionReason = "deadline-unchanged"
)

var snapshotPosterCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "provider_verification_snapshot_poster",
	Help: "The total number of provider verification snapshot poster decisions",
}, []string{"result"})

var snapshotPosterDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "provider_verification_snapshot_poster_duration",
	Help:    "Snapshot hash poster run duration in seconds",
	Buckets: prometheus.ExponentialBuckets(0.01, 2.0, 12),
})

var snapshotPosterLastSuccess = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "provider_verification_snapshot_poster_last_success",
	Help: "Unix timestamp of the last successful provider verification snapshot hash post",
})

var snapshotPosterComplianceDeadline = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "provider_verification_snapshot_poster_compliance_deadline",
	Help: "Unix timestamp of the current provider verification snapshot compliance deadline",
})

var snapshotPosterSuspended = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "provider_verification_snapshot_poster_suspended",
	Help: "Whether the current provider verification snapshot record is suspended",
})

var snapshotPosterRetryDelay = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "provider_verification_snapshot_poster_retry_delay",
	Help: "Current verification snapshot poster retry delay in seconds",
})

type Snapshotter interface {
	Build(context.Context, aepinventory.SnapshotRequest) (*aepinventory.Snapshot, error)
}

type QueryClient interface {
	ProviderSnapshot(context.Context, string) (*verificationv1.ProviderSnapshotRecord, error)
	Params(context.Context) (verificationv1.Params, error)
}

type Broadcaster interface {
	Broadcast(context.Context, ...sdk.Msg) (*sdk.TxResponse, error)
}

type PreparedSnapshotHash struct {
	Snapshot *aepinventory.Snapshot
	Payload  inventoryv1.SnapshotPayload
	Msg      *verificationv1.MsgPostSnapshotHash
}

type Decision struct {
	Post   bool
	Reason DecisionReason
}

type DecisionInput struct {
	SnapshotHash       []byte
	Record             *verificationv1.ProviderSnapshotRecord
	Now                time.Time
	PostBeforeDeadline time.Duration
}

type RunnerConfig struct {
	Snapshotter        Snapshotter
	Query              QueryClient
	Broadcaster        Broadcaster
	State              StateStore
	Log                log.Logger
	Interval           time.Duration
	IntervalJitter     time.Duration
	RetryDelay         time.Duration
	RetryJitter        time.Duration
	MaxRetries         uint
	PostBeforeDeadline time.Duration
	Now                func() time.Time
}

type Runner struct {
	cfg RunnerConfig
	run chan struct{}
}

type RunOnceResult struct {
	Prepared *PreparedSnapshotHash
	Params   verificationv1.Params
	Record   *verificationv1.ProviderSnapshotRecord
	Decision Decision
	State    State
	Response *sdk.TxResponse
}

func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Snapshotter == nil {
		return nil, errMissingSnapshotter
	}
	if cfg.Query == nil {
		return nil, errMissingQueryClient
	}
	if cfg.Broadcaster == nil {
		return nil, errMissingBroadcaster
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time {
			return time.Now().UTC()
		}
	}
	if cfg.Log == nil {
		cfg.Log = log.NewNopLogger()
	}

	runner := &Runner{
		cfg: cfg,
		run: make(chan struct{}, 1),
	}
	runner.run <- struct{}{}

	return runner, nil
}

func (r *Runner) RunOnce(ctx context.Context) (*RunOnceResult, error) {
	if r == nil {
		return nil, errMissingSnapshotter
	}

	select {
	case <-r.run:
	default:
		return nil, errPosterAlreadyRunning
	}
	defer func() {
		r.run <- struct{}{}
	}()

	return r.runOnce(ctx)
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.Interval <= 0 {
		return errInvalidRunInterval
	}
	if r.cfg.MaxRetries > 0 && r.cfg.RetryDelay <= 0 {
		return errInvalidRetryDelay
	}
	if r.cfg.IntervalJitter < 0 {
		return errInvalidRunJitter
	}
	if r.cfg.RetryJitter < 0 {
		return errInvalidRetryJitter
	}

	for {
		if err := r.runOnceWithRetry(ctx); err != nil {
			return err
		}

		if err := wait(ctx, addJitter(r.cfg.Interval, r.cfg.IntervalJitter)); err != nil {
			return nil
		}
	}
}

func (r *Runner) runOnceWithRetry(ctx context.Context) error {
	var err error

	for attempt := uint(0); attempt <= r.cfg.MaxRetries; attempt++ {
		_, err = r.RunOnce(ctx)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt == r.cfg.MaxRetries {
			r.cfg.Log.Error("verification snapshot hash post retries exhausted",
				"attempts", attempt+1,
				"err", err)
			return nil
		}

		delay := r.retryDelay(attempt)
		snapshotPosterRetryDelay.Set(delay.Seconds())
		r.cfg.Log.Error("verification snapshot hash post failed",
			"attempt", attempt+1,
			"next-retry", delay,
			"err", err)

		if waitErr := wait(ctx, delay); waitErr != nil {
			return waitErr
		}
	}

	return nil
}

func (r *Runner) runOnce(ctx context.Context) (*RunOnceResult, error) {
	start := time.Now()
	defer func() {
		snapshotPosterDuration.Observe(time.Since(start).Seconds())
	}()

	params, err := r.cfg.Query.Params(ctx)
	if err != nil {
		snapshotPosterCounter.WithLabelValues("query_error").Inc()
		return nil, err
	}

	result := &RunOnceResult{
		Params: params,
	}

	prepared, err := BuildMsg(ctx, r.cfg.Snapshotter)
	if err != nil {
		snapshotPosterCounter.WithLabelValues("snapshot_error").Inc()
		return nil, err
	}

	record, err := r.cfg.Query.ProviderSnapshot(ctx, prepared.Msg.Provider)
	if errors.Is(err, ErrProviderSnapshotNotFound) {
		record = nil
	} else if err != nil {
		snapshotPosterCounter.WithLabelValues("query_error").Inc()
		return nil, err
	}
	recordSnapshotMetrics(record)

	decision := ShouldPost(DecisionInput{
		SnapshotHash:       prepared.Msg.SnapshotHash,
		Record:             record,
		Now:                r.cfg.Now(),
		PostBeforeDeadline: r.postBeforeDeadline(params),
	})

	result.Prepared = prepared
	result.Record = record
	result.Decision = decision
	result.State = r.state(prepared, record, decision)

	if !decision.Post {
		recordPosterDecision(decision)
		r.saveState(ctx, result.State)
		r.cfg.Log.Debug("verification snapshot hash post skipped",
			"provider", prepared.Msg.Provider,
			"reason", decision.Reason,
			"snapshot-hash", hex.EncodeToString(prepared.Msg.SnapshotHash))
		return result, nil
	}

	result.Response, err = r.cfg.Broadcaster.Broadcast(ctx, prepared.Msg)
	if err != nil {
		snapshotPosterCounter.WithLabelValues("broadcast_error").Inc()
		result.State.LastError = err.Error()
		r.saveState(ctx, result.State)
		return nil, err
	}
	recordPosterDecision(decision)
	result.State.LastSuccessAt = result.State.LastAttemptAt
	result.State.LastTxHash = result.Response.TxHash
	r.saveState(ctx, result.State)
	snapshotPosterLastSuccess.Set(float64(result.State.LastSuccessAt.Unix()))
	r.cfg.Log.Info("posted verification snapshot hash",
		"provider", prepared.Msg.Provider,
		"reason", decision.Reason,
		"snapshot-hash", hex.EncodeToString(prepared.Msg.SnapshotHash),
		"tx", result.Response.TxHash)

	return result, nil
}

func recordPosterDecision(decision Decision) {
	reason := decision.Reason
	if reason == DecisionReasonNone {
		reason = "unknown"
	}

	snapshotPosterCounter.WithLabelValues(strings.ReplaceAll(string(reason), "-", "_")).Inc()
}

func (r *Runner) postBeforeDeadline(params verificationv1.Params) time.Duration {
	if r.cfg.PostBeforeDeadline > 0 {
		return r.cfg.PostBeforeDeadline
	}
	if params.SnapshotHashInterval <= 0 {
		return 0
	}

	return params.SnapshotHashInterval / 10
}

func (r *Runner) retryDelay(attempt uint) time.Duration {
	delay := r.cfg.RetryDelay
	for i := uint(0); i < attempt; i++ {
		if delay > (1<<62)-delay {
			break
		}
		delay *= 2
	}

	return addJitter(delay, r.cfg.RetryJitter)
}

func addJitter(delay time.Duration, jitter time.Duration) time.Duration {
	if delay <= 0 || jitter <= 0 {
		return delay
	}

	return delay + time.Duration(rand.Int63n(int64(jitter))) // nolint: gosec
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func BuildMsg(ctx context.Context, snapshotter Snapshotter) (*PreparedSnapshotHash, error) {
	if snapshotter == nil {
		return nil, errMissingSnapshotter
	}

	snapshot, err := snapshotter.Build(ctx, aepinventory.SnapshotRequest{})
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, errMissingSnapshot
	}
	if len(snapshot.Payload) == 0 {
		return nil, errMissingSnapshotPayload
	}
	if snapshot.Provider == "" {
		return nil, errMissingSnapshotProvider
	}
	if len(snapshot.Hash) == 0 {
		return nil, errMissingSnapshotHash
	}

	var payload inventoryv1.SnapshotPayload
	if err := payload.Unmarshal(snapshot.Payload); err != nil {
		return nil, err
	}
	if payload.Timestamp.IsZero() {
		return nil, errMissingSnapshotTimestamp
	}
	if len(payload.GetNonce()) != 0 {
		return nil, errUnexpectedSnapshotNonce
	}

	msg := &verificationv1.MsgPostSnapshotHash{
		Provider:          snapshot.Provider,
		SnapshotHash:      append([]byte(nil), snapshot.Hash...),
		ResourceSummary:   resourceSummary(payload.GetResourceSummary()),
		SnapshotTimestamp: payload.Timestamp,
	}

	return &PreparedSnapshotHash{
		Snapshot: snapshot,
		Payload:  payload,
		Msg:      msg,
	}, nil
}

func ShouldPost(input DecisionInput) Decision {
	if input.Record == nil || input.Record.GetProvider() == "" {
		return Decision{Post: true, Reason: DecisionReasonMissingRecord}
	}

	if input.Record.GetSuspended() {
		return Decision{Post: true, Reason: DecisionReasonSuspended}
	}

	if !bytes.Equal(input.SnapshotHash, input.Record.GetSnapshotHash()) {
		return Decision{Post: true, Reason: DecisionReasonHashChanged}
	}

	deadline := input.Record.GetComplianceDeadline()
	if deadline.IsZero() {
		return Decision{Post: true, Reason: DecisionReasonMissingDeadline}
	}

	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if !now.Add(input.PostBeforeDeadline).Before(deadline) {
		return Decision{Post: true, Reason: DecisionReasonDeadlineDue}
	}

	return Decision{Post: false, Reason: DecisionReasonDeadlineUnchanged}
}

func resourceSummary(summary inventoryv1.SnapshotResourceSummary) verificationv1.ResourceSummary {
	return verificationv1.ResourceSummary{
		TotalGPUs:         summary.GetTotalGPUs(),
		TotalVCPUs:        summary.GetTotalVCPUs(),
		TotalMemoryMB:     summary.GetTotalMemoryMB(),
		TotalStorageMB:    summary.GetTotalStorageMB(),
		ActiveLeases:      summary.GetActiveLeases(),
		SoftwareVersion:   summary.GetSoftwareVersion(),
		SoftwareSignature: append([]byte(nil), summary.GetSoftwareSignature()...),
	}
}

func recordSnapshotMetrics(record *verificationv1.ProviderSnapshotRecord) {
	if record == nil {
		snapshotPosterSuspended.Set(0)
		snapshotPosterComplianceDeadline.Set(0)
		return
	}

	if record.GetSuspended() {
		snapshotPosterSuspended.Set(1)
	} else {
		snapshotPosterSuspended.Set(0)
	}

	deadline := record.GetComplianceDeadline()
	if deadline.IsZero() {
		snapshotPosterComplianceDeadline.Set(0)
		return
	}

	snapshotPosterComplianceDeadline.Set(float64(deadline.Unix()))
}

func (r *Runner) state(prepared *PreparedSnapshotHash, record *verificationv1.ProviderSnapshotRecord, decision Decision) State {
	state := State{
		Version:           StateVersion,
		HashDomain:        HashDomainSnapshotV1Full,
		Provider:          prepared.Msg.Provider,
		SnapshotHash:      append([]byte(nil), prepared.Msg.SnapshotHash...),
		SnapshotTimestamp: prepared.Msg.SnapshotTimestamp,
		ResourceSummary:   prepared.Msg.ResourceSummary,
		LastAttemptAt:     r.cfg.Now(),
		LastDecision:      string(decision.Reason),
	}

	if record != nil {
		state.ComplianceDeadline = record.GetComplianceDeadline()
		state.Suspended = record.GetSuspended()
	}

	return state
}

func (r *Runner) saveState(ctx context.Context, state State) {
	if r.cfg.State == nil {
		return
	}

	if err := r.cfg.State.Set(ctx, state); err != nil {
		snapshotPosterCounter.WithLabelValues("state_error").Inc()
		r.cfg.Log.Error("saving verification snapshot poster state", "err", err)
	}
}
