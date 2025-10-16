package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	cflags "pkg.akt.dev/go/cli/flags"
)

func serviceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "service-status",
		Short:        "get service status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      cli.TxPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doServiceStatus(cmd)
		},
	}

	AddProviderOperationFlagsToCmd(cmd)
	addServiceFlags(cmd)
	addAuthFlags(cmd)

	if err := cmd.MarkFlagRequired(FlagService); err != nil {
		panic(err.Error())
	}

	return cmd
}

func doServiceStatus(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl, err := cli.ClientFromContext(ctx)
	if err != nil && !errors.Is(err, cli.ErrContextValueNotSet) {
		return err
	}
	cctx, err := cli.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	svcName, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	bid, err := cflags.BidIDFromFlags(cmd.Flags(), cflags.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), queryClientOrNil(cl), true)
	if err != nil {
		return err
	}

	result, err := gclient.ServiceStatus(cmd.Context(), bid.LeaseID(), svcName)
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, result)
}
