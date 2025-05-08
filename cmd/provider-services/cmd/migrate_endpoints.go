package cmd

import (
	"errors"

	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	"github.com/spf13/cobra"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	aclient "github.com/akash-network/provider/client"
	gwrest "github.com/akash-network/provider/gateway/rest"
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

	gclient, err := gwrest.NewClient(ctx, cl, prov, gwrest.WithJWTSigner(ajwt.NewSigner(cctx.Keyring, cctx.FromAddress)))
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
	cmd.Flags().Uint32(FlagGSeq, 1, "group sequence")

	return cmd
}
