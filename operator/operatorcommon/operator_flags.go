package operatorcommon

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	providerflags "github.com/ovrclk/provider-services/cmd/provider-services/cmd/flags"
)

const (
	flagProviderAddress = "provider"
)

func AddOperatorFlags(cmd *cobra.Command, defaultListenAddress string) {
	cmd.Flags().String(providerflags.FlagK8sManifestNS, "lease", "Cluster manifest namespace")
	if err := viper.BindPFlag(providerflags.FlagK8sManifestNS, cmd.Flags().Lookup(providerflags.FlagK8sManifestNS)); err != nil {
		panic(err)
	}

	cmd.Flags().String(providerflags.FlagListenAddress, defaultListenAddress, "listen address for web server")
	if err := viper.BindPFlag(providerflags.FlagListenAddress, cmd.Flags().Lookup(providerflags.FlagListenAddress)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagPruneInterval, 10*time.Minute, "data pruning interval")
	if err := viper.BindPFlag(providerflags.FlagPruneInterval, cmd.Flags().Lookup(providerflags.FlagPruneInterval)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagWebRefreshInterval, 5*time.Second, "web data refresh interval")
	if err := viper.BindPFlag(providerflags.FlagWebRefreshInterval, cmd.Flags().Lookup(providerflags.FlagWebRefreshInterval)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagRetryDelay, 3*time.Second, "retry delay")
	if err := viper.BindPFlag(providerflags.FlagRetryDelay, cmd.Flags().Lookup(providerflags.FlagRetryDelay)); err != nil {
		panic(err)
	}
}

func AddProviderFlag(cmd *cobra.Command) {
	cmd.Flags().String(flagProviderAddress, "", "address of associated provider in bech32")
	if err := viper.BindPFlag(flagProviderAddress, cmd.Flags().Lookup(flagProviderAddress)); err != nil {
		panic(err)
	}
}
