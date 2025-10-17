package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"
)

var errEmptyHostnames = errors.New("hostnames cannot be empty")

func migrateHostnames(cmd *cobra.Command, args []string) error {
	hostnames := args
	if len(hostnames) == 0 {
		return errEmptyHostnames
	}

	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

	paddr, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), queryClientOrNil(cl), paddr, true)
	if err != nil {
		return err
	}

	dseq, err := cmd.Flags().GetUint64("dseq")
	if err != nil {
		return err
	}

	gseq, err := cmd.Flags().GetUint32("gseq")
	if err != nil {
		return err
	}

	err = gclient.MigrateHostnames(ctx, hostnames, dseq, gseq)
	if err != nil {
		return showErrorToUser(err)
	}

	return nil
}

func MigrateHostnamesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "migrate-hostnames",
		Short:        "migrate hostnames between deployments on the same provider",
		SilenceUsage: true,
		PreRunE:      cli.TxPersistentPreRunE,
		RunE:         migrateHostnames,
	}

	cflags.AddQueryFlagsToCmd(cmd)
	AddProviderOperationFlagsToCmd(cmd)
	addCmdFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().Uint32(cflags.FlagGSeq, 1, "group sequence")

	return cmd
}
