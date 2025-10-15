package cmd

import (
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	sdk "github.com/cosmos/cosmos-sdk/types"

	apclient "pkg.akt.dev/go/provider/client"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [address]",
		Short:        "get provider status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE:      cli.QueryPersistentPreRunE,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return err
			}

			return doStatus(cmd, addr)
		},
	}

	return cmd
}

func doStatus(cmd *cobra.Command, addr sdk.Address) error {
	ctx := cmd.Context()
	cl := cli.MustLightClientFromContext(ctx)
	cctx := cl.ClientContext()

	gclient, err := apclient.NewClient(ctx, cl.Query(), addr)
	if err != nil {
		return err
	}

	result, err := gclient.Status(cmd.Context())
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, result)
}
