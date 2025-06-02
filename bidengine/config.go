package bidengine

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	types "github.com/akash-network/akash-api/go/node/types/v1beta3"
)

// Config represents the configuration parameters for the bid engine.
// It controls pricing, deposits, timeouts and provider capabilities and attributes
type Config struct {
	PricingStrategy BidPricingStrategy
	Deposit         sdk.Coin
	BidTimeout      time.Duration
	Attributes      types.Attributes
	MaxGroupVolumes int
}
