package provider

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	mtypes "pkg.akt.dev/go/node/market/v1beta5"
	attrtypes "pkg.akt.dev/go/node/types/attributes/v1"
	"pkg.akt.dev/go/node/types/constants"

	"github.com/akash-network/provider/bidengine"
	"github.com/akash-network/provider/cluster"
)

type Config struct {
	ClusterWaitReadyDuration time.Duration
	ClusterPublicHostname    string
	BidPricingStrategy       bidengine.BidPricingStrategy
	BidDeposit               sdk.Coin
	BidTimeout               time.Duration
	ManifestTimeout          time.Duration
	BalanceCheckerCfg        BalanceCheckerConfig
	Attributes               attrtypes.Attributes
	MaxGroupVolumes          int
	RPCQueryTimeout          time.Duration
	CachedResultMaxAge       time.Duration
	ReclamationWindow        *time.Duration
	cluster.Config
}

// SetClusterExternalPortQuantity sets the number of node ports the cluster
// exposes. The value is stored on the embedded cluster.Config where the
// inventory service reads it. A previous standalone field on this struct was
// never read, so the configured value never reached the inventory (support#135).
func (c *Config) SetClusterExternalPortQuantity(quantity uint) {
	c.InventoryExternalPortQuantity = quantity
}

func NewDefaultConfig() Config {
	return Config{
		ClusterWaitReadyDuration: time.Second * 10,
		BidDeposit:               mtypes.DefaultBidMinDeposit,
		BalanceCheckerCfg: BalanceCheckerConfig{
			LeaseFundsCheckInterval: 1 * time.Minute,
			WithdrawalPeriod:        24 * time.Hour,
		},
		MaxGroupVolumes: constants.DefaultMaxGroupVolumes,
		Config:          cluster.NewDefaultConfig(),
	}
}
