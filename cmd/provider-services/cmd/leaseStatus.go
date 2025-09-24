package cmd

import (
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/spf13/cobra"

	cmdcommon "github.com/akash-network/node/cmd/common"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	qclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	aclient "github.com/akash-network/provider/client"
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

	cmd.Flags().String(FlagProviderURL, "", "Provider URL to connect to directly (bypasses provider discovery)")

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	providerURL, err := cmd.Flags().GetString(FlagProviderURL)
	if err != nil {
		return err
	}

	var cl qclient.QueryClient
	var prov sdk.Address

	var leaseID mtypes.LeaseID

	if providerURL != "" {
		prov, err = providerFromFlags(cmd.Flags())
		if err != nil {
			return err
		}

		leaseID, err = leaseIDWhenProviderURLIsProvided(cmd.Flags(), cctx.GetFromAddress().String())
		if err != nil {
			return err
		}
	} else {
		cl, err = aclient.DiscoverQueryClient(ctx, cctx)
		if err != nil {
			return err
		}

		prov, err = providerFromFlags(cmd.Flags())
		if err != nil {
			return err
		}

		bid, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
		if err != nil {
			return err
		}

		leaseID = bid.LeaseID()
	}

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := providerClientFromFlags(ctx, cl, prov, opts, cmd.Flags())
	if err != nil {
		return err
	}

	result, err := gclient.LeaseStatus(ctx, leaseID)
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
