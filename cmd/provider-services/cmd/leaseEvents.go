package cmd

import (
	"context"
	"sync"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	cmdcommon "github.com/akash-network/node/cmd/common"

	qclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
)

func leaseEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-events",
		Short:        "get lease events",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseEvents(cmd)
		},
	}

	addServiceFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().BoolP("follow", "f", false, "Specify if the logs should be streamed. Defaults to false")
	cmd.Flags().Int64P("tail", "t", -1, "The number of lines from the end of the logs to show. Defaults to -1")

	if err := addProviderURLFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func doLeaseEvents(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	// Define the on-chain callback for lease events specific logic
	onChainCallback := func(ctx context.Context, cctx sdkclient.Context, cl qclient.QueryClient, flags *pflag.FlagSet) (ProviderURLHandlerResult, error) {
		dseq, err := dseqFromFlags(flags)
		if err != nil {
			return ProviderURLHandlerResult{}, err
		}

		leases, err := leasesForDeployment(ctx, cl, flags, dtypes.DeploymentID{
			Owner: cctx.GetFromAddress().String(),
			DSeq:  dseq,
		})
		if err != nil {
			return ProviderURLHandlerResult{}, markRPCServerError(err)
		}

		return ProviderURLHandlerResult{
			QueryClient: cl,
			LeaseIDs:    leases,
		}, nil
	}

	// Handle provider URL or on-chain discovery
	result, err := handleProviderURLOrOnChain(ctx, cctx, cmd.Flags(), onChainCallback)
	if err != nil {
		return err
	}

	leases := result.LeaseIDs
	cl := result.QueryClient

	svcs, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	follow, err := cmd.Flags().GetBool(flagFollow)
	if err != nil {
		return err
	}

	type streamResult struct {
		lid    mtypes.LeaseID
		error  error
		stream *apclient.LeaseKubeEvents
	}

	streams := make([]streamResult, 0, len(leases))

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	for _, lid := range leases {
		stream := streamResult{lid: lid}
		prov, _ := sdk.AccAddressFromBech32(lid.Provider)

		gclient, err := providerClientFromFlags(ctx, cl, prov, opts, cmd.Flags())
		if err == nil {
			stream.stream, stream.error = gclient.LeaseEvents(ctx, lid, svcs, follow)
		} else {
			stream.error = err
		}

		streams = append(streams, stream)
	}

	var wgStreams sync.WaitGroup
	type logEntry struct {
		apclient.LeaseEvent `json:",inline"`
		Lid                 mtypes.LeaseID `json:"lease_id"`
	}

	outch := make(chan logEntry)

	go func() {
		for evt := range outch {
			_ = cmdcommon.PrintJSON(cctx, evt)
		}
	}()

	for _, stream := range streams {
		if stream.error != nil {
			continue
		}

		wgStreams.Add(1)
		go func(stream streamResult) {
			defer wgStreams.Done()

			for res := range stream.stream.Stream {
				outch <- logEntry{
					LeaseEvent: res,
					Lid:        stream.lid,
				}
			}
		}(stream)
	}

	wgStreams.Wait()
	close(outch)

	return nil
}
