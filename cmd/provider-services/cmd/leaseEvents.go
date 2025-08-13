package cmd

import (
	"sync"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	sdk "github.com/cosmos/cosmos-sdk/types"

	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"
)

func leaseEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-events",
		Short:        "get lease events",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      cli.TxPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseEvents(cmd)
		},
	}

	addServiceFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().BoolP("follow", "f", false, "Specify if the logs should be streamed. Defaults to false")
	cmd.Flags().Int64P("tail", "t", -1, "The number of lines from the end of the logs to show. Defaults to -1")

	return cmd
}

func doLeaseEvents(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

	dseq, err := dseqFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	leases, err := leasesForDeployment(cmd.Context(), cl.Query(), cmd.Flags(), dtypes.DeploymentID{
		Owner: cctx.GetFromAddress().String(),
		DSeq:  dseq,
	})
	if err != nil {
		return markRPCServerError(err)
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
		gclient, err := apclient.NewClient(ctx, cl.Query(), prov, opts...)
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
			_ = cli.PrintJSON(cctx, evt)
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
