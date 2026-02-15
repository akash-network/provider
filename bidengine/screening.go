package bidengine

import (
	"context"
	"fmt"

	"cosmossdk.io/log"

	sdktypes "github.com/cosmos/cosmos-sdk/types"

	atypes "pkg.akt.dev/go/node/audit/v1"
	dtypes "pkg.akt.dev/go/node/deployment/v1beta4"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	atttypes "pkg.akt.dev/go/node/types/attributes/v1"

	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
)

// ScreenBidResult holds the outcome of bid screening evaluation.
type ScreenBidResult struct {
	Passed  bool
	Reasons []string
}

// ScreenBidParams holds all dependencies needed by the bid screening logic.
type ScreenBidParams struct {
	Provider        *ptypes.Provider
	Attributes      atttypes.Attributes
	MaxGroupVolumes int
	AttrService     ProviderAttrSignatureService
	HostnameService ctypes.HostnameServiceClient
	Owner           sdktypes.Address
	Hostnames       []string
	Log             log.Logger
}

// ScreenBid performs all bid eligibility checks against a GroupSpec.
// It collects all failure reasons rather than short-circuiting.
func ScreenBid(_ context.Context, group *dtypes.Group, params ScreenBidParams) (ScreenBidResult, error) {
	var reasons []string

	// Check 1: provider attribute matching
	if !group.GroupSpec.MatchAttributes(params.Provider.Attributes) {
		reasons = append(reasons, "incompatible provider attributes")
	}

	// Check 2: order attribute matching
	if !params.Attributes.SubsetOf(group.GroupSpec.Requirements.Attributes) {
		reasons = append(reasons, "incompatible order attributes")
	}

	// Check 3: resource capability matching
	attr, err := params.AttrService.GetAttributes()
	if err != nil {
		return ScreenBidResult{}, fmt.Errorf("fetching provider attributes: %w", err)
	}

	if !group.GroupSpec.MatchResourcesRequirements(attr) {
		reasons = append(reasons, "incompatible attributes for resources requirements")
	}

	// Check 4: max group volumes
	for _, resources := range group.GroupSpec.GetResourceUnits() {
		if len(resources.Storage) > params.MaxGroupVolumes {
			reasons = append(reasons, fmt.Sprintf("group volumes count exceeds limit (%d > %d)",
				len(resources.Storage), params.MaxGroupVolumes))
			break
		}
	}

	// Check 5: auditor signature requirements
	signatureRequirements := group.GroupSpec.Requirements.SignedBy
	if signatureRequirements.Size() != 0 {
		var provAttr atypes.AuditedProviders
		ownAttrs := atypes.AuditedProvider{
			Owner:      params.Provider.Owner,
			Auditor:    "",
			Attributes: params.Provider.Attributes,
		}
		provAttr = append(provAttr, ownAttrs)

		auditors := make([]string, 0)
		auditors = append(auditors, group.GroupSpec.Requirements.SignedBy.AllOf...)
		auditors = append(auditors, group.GroupSpec.Requirements.SignedBy.AnyOf...)

		gotten := make(map[string]struct{})
		for _, auditor := range auditors {
			if _, done := gotten[auditor]; done {
				continue
			}
			result, err := params.AttrService.GetAuditorAttributeSignatures(auditor)
			if err != nil {
				return ScreenBidResult{}, fmt.Errorf("fetching auditor attribute signatures: %w", err)
			}
			provAttr = append(provAttr, result...)
			gotten[auditor] = struct{}{}
		}

		if !group.GroupSpec.MatchRequirements(provAttr) {
			reasons = append(reasons, "attribute signature requirements not met")
		}
	}

	// Check 6: group spec basic validation
	if err := group.GroupSpec.ValidateBasic(); err != nil {
		reasons = append(reasons, fmt.Sprintf("group validation error: %s", err.Error()))
	}

	// Check 7: hostname availability
	if params.HostnameService != nil && len(params.Hostnames) > 0 {
		if err := params.HostnameService.CanReserveHostnames(params.Hostnames, params.Owner); err != nil {
			reasons = append(reasons, fmt.Sprintf("hostnames unavailable: %s", err.Error()))
		}
	}

	return ScreenBidResult{
		Passed:  len(reasons) == 0,
		Reasons: reasons,
	}, nil
}
