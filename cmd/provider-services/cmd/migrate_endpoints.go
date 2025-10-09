package cmd

import (
	"errors"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/spf13/cobra"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	"github.com/akash-network/provider/client"
	aclient "github.com/akash-network/provider/client"
)

var errEmptyEndpoints = errors.New("endpoints cannot be empty")

func migrateEndpoints(cmd *cobra.Command, args []string) error {
	endpoints := args
	if len(endpoints) == 0 {
		return errEmptyEndpoints
	}

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

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	opts = append(opts, apclient.WithCertQuerier(client.NewCertificateQuerier(cl)))
	gclient, err := apclient.NewClient(ctx, prov, opts...)
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

	cmd.Flags().Uint32(FlagGSeq, 1, "group sequence")

	return cmd
}
