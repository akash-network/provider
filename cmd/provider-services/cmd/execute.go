package cmd

import (
	"context"

	"github.com/spf13/cobra"

	cmtcli "github.com/cometbft/cometbft/libs/cli"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdkserver "github.com/cosmos/cosmos-sdk/server"

	"pkg.akt.dev/go/cli"

	"github.com/akash-network/provider/tools/logging"
)

// ExecuteWithCtx executes the root command. It mirrors pkg.akt.dev/go/cli.ExecuteWithCtx
// but registers the logging flags with the provider's defaults (tools/logging).
func ExecuteWithCtx(ctx context.Context, rootCmd *cobra.Command, envPrefix string) error {
	srvCtx := sdkserver.NewDefaultContext()

	ctx = context.WithValue(ctx, sdkclient.ClientContextKey, &sdkclient.Context{})
	ctx = context.WithValue(ctx, sdkserver.ServerContextKey, srvCtx)

	logging.AddFlags(rootCmd.PersistentFlags())

	executor := cmtcli.PrepareBaseCmd(rootCmd, envPrefix, cli.DefaultHome)

	return executor.ExecuteContext(ctx)
}
