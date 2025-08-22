package cmd

import (
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	cflags "pkg.akt.dev/go/cli/flags"
	apclient "pkg.akt.dev/go/provider/client"
)

func leaseStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-status",
		Short:        "get lease status",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      cli.TxPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseStatus(cmd)
		},
	}

	addLeaseFlags(cmd)
	addAuthFlags(cmd)

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

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

	gclient, err := apclient.NewClient(ctx, cl.Query(), prov, opts...)
	if err != nil {
		return err
	}

	result, err := gclient.LeaseStatus(ctx, bid.LeaseID())
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, result)
}
