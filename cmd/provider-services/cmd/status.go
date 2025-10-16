package cmd

import (
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [address]",
		Short:        "get provider status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			err := cli.QueryPersistentPreRunE(cmd, args)
			if err != nil {
				return err
			}

			// Set the hidden provider flag to the address value for internal use
			return cmd.Flags().Set(cflags.FlagProvider, args[0])
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return err
			}

			return doStatus(cmd, addr)
		},
	}

	AddProviderOperationFlagsToCmd(cmd)

	// Add hidden provider flag for internal use by setupProviderClient
	cmd.Flags().String(cflags.FlagProvider, "", "provider address")
	cmd.Flags().MarkHidden(cflags.FlagProvider)

	return cmd
}

func doStatus(cmd *cobra.Command, addr sdk.Address) error {
	ctx := cmd.Context()
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	cl, err := setupChainClient(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), cl, false)
	if err != nil {
		return err
	}

	result, err := gclient.Status(cmd.Context())
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, result)
}
