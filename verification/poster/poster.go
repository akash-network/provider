package poster

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

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
	errMissingQueryClient       = errors.New("missing verification query client")
	errMissingBroadcaster       = errors.New("missing snapshot hash broadcaster")
	errPosterAlreadyRunning     = errors.New("snapshot hash poster already running")
	errInvalidRunInterval       = errors.New("invalid snapshot hash poster run interval")
	errInvalidRetryDelay        = errors.New("invalid snapshot hash poster retry delay")
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
	Interval           time.Duration
	RetryDelay         time.Duration
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

	for {
		if err := r.runOnceWithRetry(ctx); err != nil {
			return err
		}

		if err := wait(ctx, r.cfg.Interval); err != nil {
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
			return nil
		}
		if waitErr := wait(ctx, r.cfg.RetryDelay); waitErr != nil {
			return waitErr
		}
	}

	return nil
}

func (r *Runner) runOnce(ctx context.Context) (*RunOnceResult, error) {
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

	decision := ShouldPost(DecisionInput{
		SnapshotHash:       prepared.Msg.SnapshotHash,
		Record:             record,
		Now:                r.cfg.Now(),
		PostBeforeDeadline: r.postBeforeDeadline(params),
	})

	result.Prepared = prepared
	result.Record = record
	result.Decision = decision

	if !decision.Post {
		recordPosterDecision(decision)
		return result, nil
	}

	result.Response, err = r.cfg.Broadcaster.Broadcast(ctx, prepared.Msg)
	if err != nil {
		snapshotPosterCounter.WithLabelValues("broadcast_error").Inc()
		return nil, err
	}
	recordPosterDecision(decision)

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
