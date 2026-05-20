package bidengine

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vtypes "pkg.akt.dev/go/node/verification/v1"
)

var verificationPreflightCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "provider_bid_verification_preflight",
	Help: "The total number of bid verification preflight decisions",
}, []string{"result"})

type verificationPreflightResult struct {
	allow  bool
	reason string
}

func verificationPreflightRequiresQuery(req *vtypes.VerificationRequirement) bool {
	return req != nil && req.GetMinTier() != vtypes.TierUnspecified
}

func evaluateVerificationPreflight(
	moduleActive bool,
	req *vtypes.VerificationRequirement,
	attestations []vtypes.AttestationRecord,
	grace *vtypes.ProviderVerificationGraceRecord,
) verificationPreflightResult {
	if !moduleActive {
		return verificationPreflightResult{allow: true, reason: "module_inactive"}
	}

	if !verificationPreflightRequiresQuery(req) {
		return verificationPreflightResult{allow: true, reason: "no_requirement"}
	}

	effectiveTier := bestVerificationTier(attestations)
	if grace != nil && grace.GetStatus() == vtypes.VerificationGraceStatusActive && vtypes.TierBetter(grace.GetPreservedTier(), effectiveTier) {
		effectiveTier = grace.GetPreservedTier()
	}

	if !vtypes.TierAtLeast(effectiveTier, req.GetMinTier()) {
		return verificationPreflightResult{reason: "tier"}
	}

	if !verificationCapabilitiesSatisfied(req.GetRequiredCapabilities(), attestations) {
		return verificationPreflightResult{reason: "capabilities"}
	}

	if !verificationAuditorsSatisfied(req, attestations) {
		return verificationPreflightResult{reason: "auditors"}
	}

	return verificationPreflightResult{allow: true, reason: "allow"}
}

func bestVerificationTier(attestations []vtypes.AttestationRecord) vtypes.VerificationTier {
	tier := vtypes.TierUnspecified
	for _, attestation := range attestations {
		if attestation.GetStatus() != vtypes.AttestationStatusValid {
			continue
		}
		if vtypes.TierBetter(attestation.GetTier(), tier) {
			tier = attestation.GetTier()
		}
	}
	return tier
}

func verificationCapabilitiesSatisfied(required []vtypes.CapabilityFlag, attestations []vtypes.AttestationRecord) bool {
	if len(required) == 0 {
		return true
	}

	have := make(map[vtypes.CapabilityFlag]struct{})
	for _, attestation := range attestations {
		if attestation.GetStatus() != vtypes.AttestationStatusValid {
			continue
		}
		for _, capability := range attestation.GetCapabilities() {
			have[capability] = struct{}{}
		}
	}

	for _, capability := range required {
		if capability == vtypes.CapabilityUnspecified {
			continue
		}
		if _, exists := have[capability]; !exists {
			return false
		}
	}
	return true
}

func verificationAuditorsSatisfied(req *vtypes.VerificationRequirement, attestations []vtypes.AttestationRecord) bool {
	validAuditors := make(map[string]struct{})
	for _, attestation := range attestations {
		if attestation.GetStatus() != vtypes.AttestationStatusValid {
			continue
		}
		if !vtypes.TierAtLeast(attestation.GetTier(), req.GetMinTier()) {
			continue
		}
		if auditor := attestation.GetAuditor(); auditor != "" {
			validAuditors[auditor] = struct{}{}
		}
	}

	if uint32(len(validAuditors)) < req.GetMinAuditorCount() {
		return false
	}

	required := req.GetRequiredAuditors()
	if len(required) == 0 {
		return true
	}

	if req.GetAuditorMode() == vtypes.AuditorSelectionModeAll {
		for _, auditor := range required {
			if _, exists := validAuditors[auditor]; !exists {
				return false
			}
		}
		return true
	}

	for _, auditor := range required {
		if _, exists := validAuditors[auditor]; exists {
			return true
		}
	}
	return false
}

func (o *order) shouldBidVerificationPreflight(ctx context.Context, req *vtypes.VerificationRequirement) bool {
	if !verificationPreflightRequiresQuery(req) {
		verificationPreflightCounter.WithLabelValues("no_requirement").Inc()
		return true
	}

	query := o.session.Client().Query().Verification()
	paramsResp, err := query.Params(ctx, &vtypes.QueryParamsRequest{})
	if err != nil {
		o.log.Error("verification preflight params query failed; falling open", "err", err)
		verificationPreflightCounter.WithLabelValues("fail_open").Inc()
		return true
	}
	if paramsResp == nil {
		o.log.Error("verification preflight params query returned nil; falling open")
		verificationPreflightCounter.WithLabelValues("fail_open").Inc()
		return true
	}

	params := paramsResp.GetParams()
	if !params.GetVerificationModuleActive() {
		verificationPreflightCounter.WithLabelValues("module_inactive").Inc()
		return true
	}

	provider := o.session.Provider().Owner
	attestationsResp, err := query.ProviderAttestations(ctx, &vtypes.QueryProviderAttestationsRequest{
		Provider:     provider,
		StatusFilter: vtypes.AttestationStatusValid,
	})
	if err != nil {
		o.log.Error("verification preflight attestations query failed; falling open", "err", err, "provider", provider)
		verificationPreflightCounter.WithLabelValues("fail_open").Inc()
		return true
	}
	if attestationsResp == nil {
		o.log.Error("verification preflight attestations query returned nil; falling open", "provider", provider)
		verificationPreflightCounter.WithLabelValues("fail_open").Inc()
		return true
	}

	var grace *vtypes.ProviderVerificationGraceRecord
	graceResp, err := query.ProviderVerificationGrace(ctx, &vtypes.QueryProviderVerificationGraceRequest{
		Provider: provider,
	})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			o.log.Error("verification preflight grace query failed; falling open", "err", err, "provider", provider)
			verificationPreflightCounter.WithLabelValues("fail_open").Inc()
			return true
		}
	} else if graceResp != nil {
		record := graceResp.GetGrace()
		grace = &record
	}

	result := evaluateVerificationPreflight(true, req, attestationsResp.GetAttestations(), grace)
	verificationPreflightCounter.WithLabelValues(result.reason).Inc()
	if !result.allow {
		o.log.Debug("verification requirements not met", "reason", result.reason)
	}
	return result.allow
}
