package cmd

import (
	"context"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	cmdcommon "github.com/akash-network/node/cmd/common"
	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	qclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
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

	// Define the on-chain callback for lease status specific logic
	onChainCallback := func(ctx context.Context, cctx sdkclient.Context, cl qclient.QueryClient, flags *pflag.FlagSet) (ProviderURLHandlerResult, error) {
		prov, err := providerFromFlags(flags)
		if err != nil {
			return ProviderURLHandlerResult{}, err
		}

		bid, err := mcli.BidIDFromFlags(flags, dcli.WithOwner(cctx.FromAddress))
		if err != nil {
			return ProviderURLHandlerResult{}, err
		}

		leaseID := bid.LeaseID()

		return ProviderURLHandlerResult{
			QueryClient: cl,
			LeaseID:     leaseID,
			Provider:    prov,
		}, nil
	}

	// Handle provider URL or on-chain discovery
	result, err := handleProviderURLOrOnChain(ctx, cctx, cmd.Flags(), onChainCallback)
	if err != nil {
		return err
	}

	leaseID := result.LeaseID
	prov := result.Provider
	cl := result.QueryClient

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := providerClientFromFlags(ctx, cl, prov, opts, cmd.Flags())
	if err != nil {
		return err
	}

	status, err := gclient.LeaseStatus(ctx, leaseID)
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, status)
}
