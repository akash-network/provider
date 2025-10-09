package cmd

import (
	cmdcommon "github.com/akash-network/node/cmd/common"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"
)

func leaseStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-status",
		Short:        "get lease status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseStatus(cmd)
		},
	}

	addLeaseFlags(cmd)
	addAuthFlags(cmd)

	if err := addProviderURLFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	bid, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	status, err := gclient.LeaseStatus(ctx, bid.LeaseID())
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, status)
}
