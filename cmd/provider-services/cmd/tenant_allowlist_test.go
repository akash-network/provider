package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"pkg.akt.dev/go/testutil"

	"github.com/akash-network/provider/bidengine"
)

func TestParseTenantAllowlistIsEmptyWithoutValues(t *testing.T) {
	allowlist, err := parseTenantAllowlist(nil, nil)

	require.NoError(t, err)
	require.Equal(t, bidengine.TenantAllowlist{}, allowlist)
	require.False(t, allowlist.IsEnabled())
}

func TestParseTenantAllowlistSplitsCommasAndWhitespace(t *testing.T) {
	tenantA, tenantB, tenantC := testutil.AccAddress(t).String(), testutil.AccAddress(t).String(), testutil.AccAddress(t).String()
	granterA, granterB := testutil.AccAddress(t).String(), testutil.AccAddress(t).String()

	allowlist, err := parseTenantAllowlist(
		[]string{tenantA + ", " + tenantB, "\n" + tenantC + "\t"},
		[]string{granterA + " " + granterB},
	)

	require.NoError(t, err)
	require.Equal(t, bidengine.TenantAllowlist{
		Tenants:  []string{tenantA, tenantB, tenantC},
		Granters: []string{granterA, granterB},
	}, allowlist)
}

func TestParseTenantAllowlistRejectsInvalidTenant(t *testing.T) {
	_, err := parseTenantAllowlist([]string{testutil.AccAddress(t).String() + ",akash1typo"}, nil)

	require.ErrorContains(t, err, FlagBidAllowedTenants)
	require.ErrorContains(t, err, `"akash1typo"`)
}

func TestParseTenantAllowlistRejectsInvalidGranter(t *testing.T) {
	_, err := parseTenantAllowlist(nil, []string{"not-an-address"})

	require.ErrorContains(t, err, FlagBidAllowedTenantGranters)
	require.ErrorContains(t, err, `"not-an-address"`)
}
