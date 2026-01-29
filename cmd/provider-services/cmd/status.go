package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"
	"pkg.akt.dev/go/node/client/v1beta3"
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
			err = cmd.Flags().Set(cflags.FlagProvider, args[0])
			if err != nil {
				return err
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return doStatus(cmd)
		},
	}

	AddProviderOperationFlagsToCmd(cmd)

	// Add hidden provider flag for internal use by setupProviderClient
	cmd.Flags().String(cflags.FlagProvider, "", "provider address")
	_ = cmd.Flags().MarkHidden(cflags.FlagProvider)

	return cmd
}

func doStatus(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl, err := cli.LightClientFromContext(ctx)
	if err != nil && !errors.Is(err, cli.ErrContextValueNotSet) {
		return err
	}
	cctx, err := cli.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	var queryClient v1beta3.QueryClient
	if cl != nil {
		queryClient = cl.Query()
	}

	paddr, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), queryClient, paddr, false)
	if err != nil {
		return err
	}

	result, err := gclient.Status(cmd.Context())
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, result)
}
