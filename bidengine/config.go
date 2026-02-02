package bidengine

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	atttypes "pkg.akt.dev/go/node/types/attributes/v1"
)

// Config represents the configuration parameters for the bid engine.
// It controls pricing, deposits, timeouts and provider capabilities and attributes
type Config struct {
	PricingStrategy BidPricingStrategy
	Deposit         sdk.Coin
	BidTimeout      time.Duration
	Attributes      atttypes.Attributes
	MaxGroupVolumes int
}
