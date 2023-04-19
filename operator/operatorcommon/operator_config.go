package operatorcommon

import (
	"github.com/akash-network/provider/cluster/kube"
	"time"

	"github.com/spf13/viper"

	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
)

type OperatorConfig struct {
	PruneInterval      time.Duration
	WebRefreshInterval time.Duration
	RetryDelay         time.Duration
	ProviderAddress    string
	ClientConfig       kube.ClientConfig
}

func GetOperatorConfigFromViper() OperatorConfig {
	var sslCfg kube.Ssl
	if viper.GetBool(providerflags.FlagSslEnabled) {
		sslCfg = kube.Ssl{
			IssuerName: viper.GetString(providerflags.FlagSslIssuerName),
			IssuerType: viper.GetString(providerflags.FlagSslIssuerType),
		}
	}
	ccfg := kube.ClientConfig{Ssl: sslCfg}

	return OperatorConfig{
		PruneInterval:      viper.GetDuration(providerflags.FlagPruneInterval),
		WebRefreshInterval: viper.GetDuration(providerflags.FlagWebRefreshInterval),
		RetryDelay:         viper.GetDuration(providerflags.FlagRetryDelay),
		ProviderAddress:    viper.GetString(flagProviderAddress),
		ClientConfig:       ccfg,
	}
}
