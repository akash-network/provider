package cmd

import (
	"fmt"
	"strings"
	"unicode"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/akash-network/provider/bidengine"
)

func parseTenantAllowlist(tenants, granters []string) (bidengine.TenantAllowlist, error) {
	parsedTenants, err := parseAddressList(FlagBidAllowedTenants, tenants)
	if err != nil {
		return bidengine.TenantAllowlist{}, err
	}

	parsedGranters, err := parseAddressList(FlagBidAllowedTenantGranters, granters)
	if err != nil {
		return bidengine.TenantAllowlist{}, err
	}

	return bidengine.TenantAllowlist{Tenants: parsedTenants, Granters: parsedGranters}, nil
}

// parseAddressList accepts comma and whitespace separators, since viper splits env values on whitespace only.
func parseAddressList(flag string, values []string) ([]string, error) {
	var addresses []string
	for _, value := range values {
		for _, address := range strings.FieldsFunc(value, isAddressSeparator) {
			decoded, err := sdk.AccAddressFromBech32(address)
			if err != nil {
				return nil, fmt.Errorf(`flag "%s" contains invalid address %q: %w`, flag, address, err)
			}
			addresses = append(addresses, decoded.String())
		}
	}

	return addresses, nil
}

func isAddressSeparator(r rune) bool {
	return r == ',' || unicode.IsSpace(r)
}
