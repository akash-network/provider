package cmd

import (
	"github.com/cosmos/cosmos-sdk/client/debug"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/rpc"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	"github.com/ovrclk/akash/app"
	"github.com/ovrclk/akash/sdkutil"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	tmcli "github.com/tendermint/tendermint/libs/cli"

	acmd "github.com/ovrclk/akash/cmd/akash/cmd"
	ecmd "github.com/ovrclk/akash/events/cmd"

	"github.com/ovrclk/provider-services/operator"
	"github.com/ovrclk/provider-services/operator/hostnameoperator"
	"github.com/ovrclk/provider-services/operator/ipoperator"
	"github.com/ovrclk/provider-services/version"
)

func NewRootCmd() *cobra.Command {
	sdkutil.InitSDKConfig()

	encodingConfig := app.MakeEncodingConfig()

	cmd := &cobra.Command{
		Use:               "provider-services",
		Short:             "Provider services commands",
		SilenceUsage:      true,
		PersistentPreRunE: acmd.GetPersistentPreRunE(encodingConfig, []string{"AP", "AKASH"}),
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
	cmd.AddCommand(version.NewVersionCommand())

	cmd.AddCommand(acmd.QueryCmd())
	cmd.AddCommand(acmd.TxCmd())

	cmd.AddCommand(nodeCmd())

	cmd.AddCommand(ecmd.EventCmd())
	cmd.AddCommand(keys.Commands(app.DefaultHome))
	cmd.AddCommand(genutilcli.InitCmd(app.ModuleBasics(), app.DefaultHome))
	cmd.AddCommand(genutilcli.CollectGenTxsCmd(banktypes.GenesisBalancesIterator{}, app.DefaultHome))
	cmd.AddCommand(genutilcli.GenTxCmd(app.ModuleBasics(), encodingConfig.TxConfig, banktypes.GenesisBalancesIterator{}, app.DefaultHome))
	cmd.AddCommand(genutilcli.ValidateGenesisCmd(app.ModuleBasics()))
	cmd.AddCommand(acmd.AddGenesisAccountCmd(app.DefaultHome))
	cmd.AddCommand(tmcli.NewCompletionCmd(cmd, true))
	cmd.AddCommand(debug.Cmd())

	return cmd
}

func nodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "operations with akash RPC node",
	}

	cmd.AddCommand(rpc.StatusCommand())
	cmd.AddCommand(genutilcli.MigrateGenesisCmd())

	return cmd
}
