package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"

	apclient "pkg.akt.dev/go/provider/client"
)

var errEmptyEndpoints = errors.New("endpoints cannot be empty")

func migrateEndpoints(cmd *cobra.Command, args []string) error {
	endpoints := args
	if len(endpoints) == 0 {
		return errEmptyEndpoints
	}

	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

	prov, err := providerFromFlags(cmd.Flags())
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

	dseq, err := cmd.Flags().GetUint64("dseq")
	if err != nil {
		return err
	}

	gseq, err := cmd.Flags().GetUint32("gseq")
	if err != nil {
		return err
	}

	err = gclient.MigrateEndpoints(cmd.Context(), endpoints, dseq, gseq)
	if err != nil {
		return showErrorToUser(err)
	}

	return nil

}

func MigrateEndpointsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "migrate-endpoints",
		Short:        "migrate endpoints between deployments on the same provider",
		SilenceUsage: true,
		RunE:         migrateEndpoints,
	}

	addCmdFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().Uint32(cflags.FlagGSeq, 1, "group sequence")

	return cmd
}
