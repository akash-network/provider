package bidengine

import (
	"context"
	"fmt"
	"slices"

	feegranttypes "cosmossdk.io/x/feegrant"
	"github.com/cosmos/cosmos-sdk/types/query"
)

// TenantAllowlist restricts bidding to the listed tenants and to tenants holding a fee grant from a listed granter.
// An empty allowlist lets every tenant through.
type TenantAllowlist struct {
	Tenants  []string
	Granters []string
}

func (a TenantAllowlist) IsEnabled() bool {
	return len(a.Tenants) > 0 || len(a.Granters) > 0
}

func (a TenantAllowlist) listsTenant(tenant string) bool {
	return slices.Contains(a.Tenants, tenant)
}

func (a TenantAllowlist) hasGrantFromListedGranter(ctx context.Context, feegrants feegranttypes.QueryClient, tenant string) (bool, error) {
	req := &feegranttypes.QueryAllowancesRequest{Grantee: tenant}
	for {
		res, err := feegrants.Allowances(ctx, req)
		if err != nil {
			return false, fmt.Errorf("querying fee grants of tenant %s: %w", tenant, err)
		}

		for _, grant := range res.Allowances {
			if slices.Contains(a.Granters, grant.Granter) {
				return true, nil
			}
		}

		if res.Pagination == nil || len(res.Pagination.NextKey) == 0 {
			return false, nil
		}

		req = &feegranttypes.QueryAllowancesRequest{Grantee: tenant, Pagination: &query.PageRequest{Key: res.Pagination.NextKey}}
	}
}
