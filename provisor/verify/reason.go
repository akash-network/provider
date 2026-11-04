package verify

import "github.com/akash-network/provider/provisor/release"

// Reason is a closed enum naming exactly why a Verify call refused to
// produce a manifest. Every value is distinct and meaningful on its own; a
// later phase surfaces each as its own condition on a Kubernetes object, so
// none of these ever collapse into a generic failure.
type Reason string

const (
	// ReasonMalformedDocument covers any JSON parsing failure at any layer:
	// the outer envelope, its base64 payload, or the document that payload
	// decodes to.
	ReasonMalformedDocument Reason = "MalformedDocument"

	// ReasonSignatureThresholdNotMet means at least one claimed signer was
	// recognized, but too few of the claimed signatures validated to reach
	// the required threshold.
	ReasonSignatureThresholdNotMet Reason = "SignatureThresholdNotMet"

	// ReasonUnknownSigningKey means none of the claimed signers were
	// recognized at all, which is a stronger and more specific failure than
	// simply falling short of a threshold.
	ReasonUnknownSigningKey Reason = "UnknownSigningKey"

	ReasonKeySetExpired  Reason = "KeySetExpired"
	ReasonKeySetReplayed Reason = "KeySetReplayed"
	ReasonDigestMismatch Reason = "DigestMismatch"

	ReasonTransferFailed   Reason = "TransferFailed"
	ReasonTransferTooLarge Reason = "TransferTooLarge"
	ReasonTransferTooSlow  Reason = "TransferTooSlow"
	ReasonClockSkew        Reason = "ClockSkew"

	// These mirror release.Decision's refusal values exactly, string for
	// string, so a release.Decision converts directly into a Reason without
	// a translation table.
	ReasonExpired            Reason = Reason(release.Expired)
	ReasonNotMonotonic       Reason = Reason(release.NotMonotonic)
	ReasonBelowUpgradeFloor  Reason = Reason(release.BelowUpgradeFloor)
	ReasonUnsupportedSchema  Reason = Reason(release.UnsupportedSchema)
	ReasonUnpinnedReference  Reason = Reason(release.UnpinnedReference)
	ReasonDisallowedRegistry Reason = Reason(release.DisallowedRegistry)
)

// Result is the outcome of a Verify call: either a verified Manifest with a
// zero Reason, or a nil Manifest with exactly one Reason set.
type Result struct {
	Manifest *release.Manifest
	Reason   Reason
}
