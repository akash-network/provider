package operatorcommon

import (
	"time"

	"github.com/spf13/viper"

	providerflags "github.com/ovrclk/provider-services/cmd/provider-services/cmd/flags"
)

type OperatorConfig struct {
	PruneInterval      time.Duration
	WebRefreshInterval time.Duration
	RetryDelay         time.Duration
	ProviderAddress    string
}

func GetOperatorConfigFromViper() OperatorConfig {
	return OperatorConfig{
		PruneInterval:      viper.GetDuration(providerflags.FlagPruneInterval),
		WebRefreshInterval: viper.GetDuration(providerflags.FlagWebRefreshInterval),
		RetryDelay:         viper.GetDuration(providerflags.FlagRetryDelay),
		ProviderAddress:    viper.GetString(flagProviderAddress),
	}
}
