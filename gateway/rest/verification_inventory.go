package rest

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/akash-network/provider/verification/inventory"
)

type verificationInventoryStatusSource interface {
	Latest(context.Context, string) (inventory.SnapshotRecord, bool, error)
}

type verificationInventoryStatus struct {
	Provider      string                          `json:"provider"`
	Hash          string                          `json:"hash"`
	Signature     string                          `json:"signature"`
	SchemaVersion uint32                          `json:"schema_version"`
	CreatedAt     time.Time                       `json:"created_at"`
	Validation    verificationInventoryValidation `json:"validation"`
}

type verificationInventoryValidation struct {
	Status      inventory.SnapshotValidationStatus `json:"status"`
	Error       string                             `json:"error,omitempty"`
	ValidatedAt *time.Time                         `json:"validated_at,omitempty"`
}

type verificationInventoryStatusKey struct{}

func SetVerificationInventoryStatusSource(cfg map[interface{}]interface{}, source verificationInventoryStatusSource) {
	if cfg == nil || source == nil {
		return
	}

	cfg[verificationInventoryStatusKey{}] = source
}

func verificationInventoryStatusSourceFromConfig(cfg map[interface{}]interface{}) verificationInventoryStatusSource {
	if cfg == nil {
		return nil
	}

	source, _ := cfg[verificationInventoryStatusKey{}].(verificationInventoryStatusSource)
	return source
}

func latestVerificationInventoryStatus(
	ctx context.Context,
	source verificationInventoryStatusSource,
	provider string,
) (*verificationInventoryStatus, error) {
	if source == nil {
		return nil, nil
	}

	record, ok, err := source.Latest(ctx, provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return &verificationInventoryStatus{
		Provider:      record.Snapshot.Provider,
		Hash:          base64.StdEncoding.EncodeToString(record.Snapshot.Hash),
		Signature:     base64.StdEncoding.EncodeToString(record.Snapshot.Signature),
		SchemaVersion: record.SchemaVersion,
		CreatedAt:     record.CreatedAt,
		Validation:    verificationValidationStatus(record.Validation),
	}, nil
}

func verificationValidationStatus(validation inventory.SnapshotValidation) verificationInventoryValidation {
	status := validation.Status
	if status == "" {
		status = inventory.SnapshotValidationStatusUnvalidated
	}

	result := verificationInventoryValidation{
		Status: status,
		Error:  validation.Error,
	}
	if !validation.ValidatedAt.IsZero() {
		validatedAt := validation.ValidatedAt
		result.ValidatedAt = &validatedAt
	}

	return result
}
