package cmd

import (
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	cflags "pkg.akt.dev/go/cli/flags"
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
	if err := addNoChainFlag(cmd); err != nil {
		panic(err)
	}

	if err := addProviderURLFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func doLeaseStatus(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

	qclient, err := setupChainClient(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	bid, err := cflags.BidIDFromFlags(cmd.Flags(), cflags.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), qclient, true)
	if err != nil {
		return err
	}

	status, err := gclient.LeaseStatus(ctx, bid.LeaseID())
	if err != nil {
		return showErrorToUser(err)
	}

	return cli.PrintJSON(cctx, status)
}
