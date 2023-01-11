package provider

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	types "github.com/akash-network/node/types/v1beta2"
	"github.com/akash-network/node/validation/constants"
	mtypes "github.com/akash-network/node/x/market/types/v1beta2"

	"github.com/akash-network/provider/bidengine"
)

type Config struct {
	ClusterWaitReadyDuration        time.Duration
	ClusterPublicHostname           string
	ClusterExternalPortQuantity     uint
	InventoryResourcePollPeriod     time.Duration
	InventoryResourceDebugFrequency uint
	BidPricingStrategy              bidengine.BidPricingStrategy
	BidDeposit                      sdk.Coin
	CPUCommitLevel                  float64
	MemoryCommitLevel               float64
	StorageCommitLevel              float64
	MaxGroupVolumes                 int
	BlockedHostnames                []string
	BidTimeout                      time.Duration
	ManifestTimeout                 time.Duration
	BalanceCheckerCfg               BalanceCheckerConfig
	Attributes                      types.Attributes
	DeploymentIngressStaticHosts    bool
	DeploymentIngressDomain         string
	ClusterSettings                 map[interface{}]interface{}
	RPCQueryTimeout                 time.Duration
	CachedResultMaxAge              time.Duration
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
	}
}
