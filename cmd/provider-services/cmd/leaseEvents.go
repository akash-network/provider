package cmd

import (
	"sync"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	cmdcommon "github.com/akash-network/node/cmd/common"

	qclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	aclient "github.com/akash-network/provider/client"
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
	cmd.Flags().String(FlagProviderURL, "", "Provider URL to connect to directly (bypasses provider discovery)")

	return cmd
}

func doLeaseEvents(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	providerURL, err := cmd.Flags().GetString(FlagProviderURL)
	if err != nil {
		return err
	}

	var leases []mtypes.LeaseID
	var cl qclient.QueryClient

	cl, err = aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	if providerURL != "" {
		leaseID, err := constructLeaseIDFromProviderURL(cmd.Flags(), cctx.GetFromAddress().String())
		if err != nil {
			return err
		}

		leases = []mtypes.LeaseID{leaseID}
	} else {
		dseq, err := dseqFromFlags(cmd.Flags())
		if err != nil {
			return err
		}

		leases, err = leasesForDeployment(cmd.Context(), cl, cmd.Flags(), dtypes.DeploymentID{
			Owner: cctx.GetFromAddress().String(),
			DSeq:  dseq,
		})
		if err != nil {
			return markRPCServerError(err)
		}
	}

	svcs, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	follow, err := cmd.Flags().GetBool(flagFollow)
	if err != nil {
		return err
	}

	type result struct {
		lid    mtypes.LeaseID
		error  error
		stream *apclient.LeaseKubeEvents
	}

	streams := make([]result, 0, len(leases))

	opts, err := loadAuthOpts(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	for _, lid := range leases {
		stream := result{lid: lid}
		prov, _ := sdk.AccAddressFromBech32(lid.Provider)

		var gclient apclient.Client
		if providerURL != "" {
			gclient, err = apclient.NewClientOffChain(ctx, providerURL, prov, opts...)
		} else {
			gclient, err = apclient.NewClient(ctx, cl, prov, opts...)
		}

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
		go func(stream result) {
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
