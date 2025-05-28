package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/sdl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func bidPreCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "bid-pre-check <manifest-file>",
		Short:        "perform a pre-bid check using a manifest file",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPreBidCheck(cmd, args[0])
		},
	}

	return cmd
}

func doPreBidCheck(cmd *cobra.Command, manifestPath string) error {
	ctx := context.Background()

	// Load the manifest file
	sdlFile, err := sdl.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest file: %w", err)
	}
	groups, err := sdlFile.DeploymentGroups()
	if err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	groupPointers := make([]dtypes.GroupSpec, 0, len(groups))

	for _, group := range groups {
		groupPointers = append(groupPointers, *group)
	}

	// Set up TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // For development only
	}
	creds := credentials.NewTLS(tlsConfig)

	// Set up gRPC connection with TLS
	conn, err := grpc.DialContext(ctx, "localhost:8444", grpc.WithTransportCredentials(creds))
	if err != nil {
		return fmt.Errorf("failed to connect to provider gRPC: %w", err)
	}
	defer conn.Close()

	client := providerv1.NewProviderRPCClient(conn)

	// Build and send the request
	req := &providerv1.BidPreCheckRequest{
		Groups: groupPointers,
	}
	resp, err := client.BidPreCheck(ctx, req)
	if err != nil {
		return fmt.Errorf("PreBidCheck RPC failed: %w", err)
	}

	// Print the response as JSON
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}
