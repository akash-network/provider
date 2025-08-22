package cmd

import (
	apclient "github.com/akash-network/akash-api/go/provider/client"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
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

	// Get provider URL flag
	providerURL, err := cmd.Flags().GetString(FlagProviderURL)
	if err != nil {
		return err
	}

	var cl qclient.QueryClient
	var prov sdk.Address

	// Always get query client for certificate verification (needed even with provider URL)
	cl, err = aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	var leaseID mtypes.LeaseID

	if providerURL != "" {
		// When provider URL is provided, bypass provider discovery and construct lease ID directly
		// Validate that all required flags are provided
		if !cmd.Flags().Changed(FlagProvider) {
			return errors.Errorf("provider flag is required when using provider-url")
		}
		if !cmd.Flags().Changed(FlagDSeq) {
			return errors.Errorf("dseq flag is required when using provider-url")
		}
		if !cmd.Flags().Changed(FlagGSeq) {
			return errors.Errorf("gseq flag is required when using provider-url")
		}
		if !cmd.Flags().Changed(FlagOSeq) {
			return errors.Errorf("oseq flag is required when using provider-url")
		}

		prov, err = providerFromFlags(cmd.Flags())
		if err != nil {
			return err
		}

		leaseID, err = leaseIDFromFlags(cmd.Flags(), cctx.GetFromAddress().String())
		if err != nil {
			return err
		}
	} else {
		// Original discovery behavior
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

	var gclient apclient.Client
	if providerURL != "" {
		// Use NewClientV2 with provider URL, but still provide query client for cert verification
		gclient, err = apclient.NewClientV2(ctx, cl, providerURL, prov, opts...)
	} else {
		// Use original NewClient method
		gclient, err = apclient.NewClient(ctx, cl, prov, opts...)
	}
	if err != nil {
		return err
	}

	result, err := gclient.LeaseStatus(ctx, leaseID)
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
