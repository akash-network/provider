package cmd

import (
	"github.com/ovrclk/akash/app"
	"github.com/ovrclk/akash/sdkutil"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/ovrclk/provider-services/operator"
	"github.com/ovrclk/provider-services/operator/hostnameoperator"
	"github.com/ovrclk/provider-services/operator/ipoperator"

	"github.com/cosmos/cosmos-sdk/client/flags"

	acmd "github.com/ovrclk/akash/cmd/akash/cmd"
)

func NewRootCmd() *cobra.Command {
	sdkutil.InitSDKConfig()

	encodingConfig := app.MakeEncodingConfig()

	cmd := &cobra.Command{
		Use:               "provider-services",
		Short:             "Provider services commands",
		SilenceUsage:      true,
		PersistentPreRunE: acmd.GetPersistentPreRunE(encodingConfig),
	}

	cmd.PersistentFlags().String(flags.FlagNode, "http://localhost:26657", "The node address")
	if err := viper.BindPFlag(flags.FlagNode, cmd.PersistentFlags().Lookup(flags.FlagNode)); err != nil {
		return nil
	}

	cmd.AddCommand(SendManifestCmd())
	cmd.AddCommand(statusCmd())
	cmd.AddCommand(leaseStatusCmd())
	cmd.AddCommand(leaseEventsCmd())
	cmd.AddCommand(leaseLogsCmd())
	cmd.AddCommand(serviceStatusCmd())
	cmd.AddCommand(RunCmd())
	cmd.AddCommand(LeaseShellCmd())
	cmd.AddCommand(hostnameoperator.Cmd())
	cmd.AddCommand(ipoperator.Cmd())
	cmd.AddCommand(MigrateHostnamesCmd())
	cmd.AddCommand(AuthServerCmd())
	cmd.AddCommand(clusterNSCmd())
	cmd.AddCommand(migrate())
	cmd.AddCommand(RunResourceServerCmd())
	cmd.AddCommand(MigrateEndpointsCmd())
	cmd.AddCommand(operator.Cmd())

	return cmd
}
