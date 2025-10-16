package cmd

import (
	"errors"
	"sync"

	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"
)

func leaseEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-events",
		Short:        "get lease events",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      ProviderPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseEvents(cmd)
		},
	}

	AddProviderOperationFlagsToCmd(cmd)
	addServiceFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().BoolP("follow", "f", false, "Specify if the logs should be streamed. Defaults to false")
	cmd.Flags().Int64P("tail", "t", -1, "The number of lines from the end of the logs to show. Defaults to -1")

	return cmd
}

func doLeaseEvents(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl, err := cli.ClientFromContext(ctx)
	if err != nil && !errors.Is(err, cli.ErrContextValueNotSet) {
		return err
	}
	cctx, err := cli.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	leases, err := leasesForDeployment(cmd.Context(), cctx, cmd.Flags(), queryClientOrNil(cl))
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

	for _, lid := range leases {
		stream := result{lid: lid}
		gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), cl.Query(), true)
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
