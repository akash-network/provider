package cmd

import (
	apclient "github.com/akash-network/akash-api/go/provider/client"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	cmdcommon "github.com/akash-network/node/cmd/common"

	"github.com/akash-network/provider/client"
	aclient "github.com/akash-network/provider/client"
)

func statusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status [address]",
		Short:        "get provider status",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return err
			}

			return doStatus(cmd, addr)
		},
	}

	return cmd
}

func doStatus(cmd *cobra.Command, addr sdk.Address) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	gclient, err := apclient.NewClient(ctx, addr, apclient.WithCertQuerier(client.NewCertificateQuerier(cl)))
	if err != nil {
		return err
	}

	result, err := gclient.Status(cmd.Context())
	if err != nil {
		return showErrorToUser(err)
	}

	return cmdcommon.PrintJSON(cctx, result)
}
