package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	cflags "pkg.akt.dev/go/cli/flags"
	acmd "pkg.akt.dev/node/cmd/akash/cmd"

	tmcli "github.com/cometbft/cometbft/libs/cli"
	"github.com/cosmos/cosmos-sdk/client/debug"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"

	"pkg.akt.dev/go/cli"
	"pkg.akt.dev/go/sdkutil"
	"pkg.akt.dev/node/app"

	"github.com/akash-network/provider/operator"
	"github.com/akash-network/provider/operator/hostname"
	"github.com/akash-network/provider/operator/ip"
	"github.com/akash-network/provider/version"
)

func NewRootCmd() *cobra.Command {
	encodingConfig := sdkutil.MakeEncodingConfig()
	app.ModuleBasics().RegisterInterfaces(encodingConfig.InterfaceRegistry)

	cmd := &cobra.Command{
		Use:               "provider-services",
		Short:             "Provider services commands",
		SilenceUsage:      true,
		PersistentPreRunE: cli.GetPersistentPreRunE(encodingConfig, []string{"AP", "AKASH"}, cli.DefaultHome),
	}

	cmd.PersistentFlags().String(cflags.FlagNode, "http://localhost:26657", "The node address")
	if err := viper.BindPFlag(cflags.FlagNode, cmd.PersistentFlags().Lookup(cflags.FlagNode)); err != nil {
		return nil
	}

	cmd.AddCommand(ManifestCmds()...)
	cmd.AddCommand(statusCmd())
	cmd.AddCommand(leaseStatusCmd())
	cmd.AddCommand(leaseEventsCmd())
	cmd.AddCommand(leaseLogsCmd())
	cmd.AddCommand(serviceStatusCmd())
	cmd.AddCommand(RunCmd())
	cmd.AddCommand(LeaseShellCmd())
	cmd.AddCommand(hostname.Cmd())
	cmd.AddCommand(ip.Cmd())
	cmd.AddCommand(clusterNSCmd())
	cmd.AddCommand(migrate())
	cmd.AddCommand(SDL2ManifestCmd())
	cmd.AddCommand(MigrateHostnamesCmd())
	cmd.AddCommand(MigrateEndpointsCmd())

	cmd.AddCommand(operator.OperatorsCmd())
	cmd.AddCommand(operator.ToolsCmd())

	cmd.AddCommand(version.NewVersionCommand())

	cmd.AddCommand(cli.QueryCmd())
	cmd.AddCommand(cli.TxCmd())

	cmd.AddCommand(nodeCmd())

	cmd.AddCommand(cli.EventsCmd())
	cmd.AddCommand(cli.KeysCmds())
	cmd.AddCommand(genutilcli.InitCmd(app.ModuleBasics(), app.DefaultHome))
	// cmd.AddCommand(genutilcli.CollectGenTxsCmd(banktypes.GenesisBalancesIterator{}, app.DefaultHome))
	// cmd.AddCommand(genutilcli.GenTxCmd(app.ModuleBasics(), encodingConfig.TxConfig, banktypes.GenesisBalancesIterator{}, app.DefaultHome))
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

	// cmd.AddCommand(rpc.StatusCommand())
	// cmd.AddCommand(genutilcli.MigrateGenesisCmd())

	return cmd
}
