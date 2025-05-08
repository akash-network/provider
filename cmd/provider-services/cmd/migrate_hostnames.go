package cmd

import (
	"errors"

	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	"github.com/spf13/cobra"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	aclient "github.com/akash-network/provider/client"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

var errEmptyHostnames = errors.New("hostnames cannot be empty")

func migrateHostnames(cmd *cobra.Command, args []string) error {
	hostnames := args
	if len(hostnames) == 0 {
		return errEmptyHostnames
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
		RunE:         migrateHostnames,
	}

	addCmdFlags(cmd)
	cmd.Flags().Uint32(FlagGSeq, 1, "group sequence")

	return cmd
}
