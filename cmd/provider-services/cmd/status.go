package cmd

import (
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	cmdcommon "github.com/akash-network/node/cmd/common"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [address]",
		Short:        "get provider status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Set the hidden provider flag to the address value for internal use
			return cmd.Flags().Set(FlagProvider, args[0])
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return err
			}

			return doStatus(cmd, addr)
		},
	}

	// Add hidden provider flag for internal use by setupProviderClient
	cmd.Flags().String(FlagProvider, "", "provider address")
	cmd.Flags().MarkHidden(FlagProvider)

	if err := addNoChainFlag(cmd); err != nil {
		panic(err)
	}

	if err := addProviderURLFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func doStatus(cmd *cobra.Command, addr sdk.Address) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

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

	return cmdcommon.PrintJSON(cctx, result)
}
