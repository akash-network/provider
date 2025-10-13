package cmd

import (
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"

	cmdcommon "github.com/akash-network/node/cmd/common"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"
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

	cl, err := setupChainClient(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	svcName, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	bid, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), cl, true)
	if err != nil {
		return err
	}

	result, err := gclient.ServiceStatus(cmd.Context(), bid.LeaseID(), svcName)
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
