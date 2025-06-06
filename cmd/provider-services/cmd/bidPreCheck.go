package cmd

import (
	"encoding/json"
	"fmt"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/akash-network/node/sdl"
	aclient "github.com/akash-network/provider/client"
)

func bidPreCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "bid-pre-check <address> <manifest-file>",
		Short:        "perform a pre-bid check using a manifest file",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPreBidCheck(cmd, args[0], args[1])
		},
	}

	return cmd
}

func doPreBidCheck(cmd *cobra.Command, addr, manifestPath string) error {
	ctx := cmd.Context()

	cctx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	// Load the manifest file
	sdlFile, err := sdl.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file: %w", err)
	}
	groups, err := sdlFile.DeploymentGroups()
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	prov, err := sdk.AccAddressFromBech32(addr)
	if err != nil {
		return fmt.Errorf("invalid provider address: %w", err)
	}

	// Create provider client with authentication
	gclient, err := apclient.NewClient(ctx, cl, prov)
	if err != nil {
		return fmt.Errorf("failed to create provider client: %w", err)
	}

	result, err := gclient.Validate(ctx, groups)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
