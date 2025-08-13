package cmd

import (
	"github.com/spf13/cobra"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	cmdcommon "pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"
	apclient "pkg.akt.dev/go/provider/client"

	aclient "pkg.akt.dev/go/node/client/discovery"
)

func serviceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "service-status",
		Short:        "get service status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doServiceStatus(cmd)
		},
	}

	addServiceFlags(cmd)
	addAuthFlags(cmd)

	if err := cmd.MarkFlagRequired(FlagService); err != nil {
		panic(err.Error())
	}

	return cmd
}

func doServiceStatus(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	svcName, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	prov, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	bid, err := cflags.BidIDFromFlags(cmd.Flags(), cflags.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := apclient.NewClient(ctx, cl, prov, opts...)
	if err != nil {
		return err
	}

	result, err := gclient.ServiceStatus(cmd.Context(), bid.LeaseID(), svcName)
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
