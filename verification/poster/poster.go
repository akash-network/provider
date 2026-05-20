package poster

import (
	"bytes"
	"context"
	"errors"
	"time"

	inventoryv1 "pkg.akt.dev/go/inventory/v1"
	verificationv1 "pkg.akt.dev/go/node/verification/v1"

	aepinventory "github.com/akash-network/provider/verification/inventory"
)

var (
	errMissingSnapshotter       = errors.New("missing inventory snapshotter")
	errMissingSnapshot          = errors.New("missing inventory snapshot")
	errMissingSnapshotPayload   = errors.New("missing inventory snapshot payload")
	errMissingSnapshotProvider  = errors.New("missing inventory snapshot provider")
	errMissingSnapshotHash      = errors.New("missing inventory snapshot hash")
	errMissingSnapshotTimestamp = errors.New("missing inventory snapshot timestamp")
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

type Snapshotter interface {
	Build(context.Context, aepinventory.SnapshotRequest) (*aepinventory.Snapshot, error)
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
