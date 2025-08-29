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
	ClusterWaitReadyDuration    time.Duration
	ClusterPublicHostname       string
	ClusterExternalPortQuantity uint
	BidPricingStrategy          bidengine.BidPricingStrategy
	BidDeposit                  sdk.Coin
	BidTimeout                  time.Duration
	ManifestTimeout             time.Duration
	BalanceCheckerCfg           BalanceCheckerConfig
	Attributes                  attrtypes.Attributes
	MaxGroupVolumes             int
	RPCQueryTimeout             time.Duration
	CachedResultMaxAge          time.Duration
	cluster.Config
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
