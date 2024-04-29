package cmd

import (
	"crypto/tls"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"

	cmdcommon "github.com/akash-network/node/cmd/common"
	cutils "github.com/akash-network/node/x/cert/utils"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	aclient "github.com/akash-network/provider/client"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

func leaseStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-status",
		Short:        "get lease status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doLeaseStatus(cmd)
		},
	}

	addLeaseFlags(cmd)

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	prov, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	bid, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	cert, err := cutils.LoadAndQueryCertificateForAccount(cmd.Context(), cctx, nil)
	if err != nil {
		return markRPCServerError(err)
	}

	gclient, err := gwrest.NewClient(ctx, cl, prov, []tls.Certificate{cert})
	if err != nil {
		return err
	}

	result, err := gclient.LeaseStatus(cmd.Context(), bid.LeaseID())
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
