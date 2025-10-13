package cmd

import (
	"fmt"
	"sync"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"

	mtypes "pkg.akt.dev/go/node/market/v1"
	apclient "pkg.akt.dev/go/provider/client"
)

func leaseLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-logs",
		Short:        "get lease logs",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      cli.TxPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseLogs(cmd)
		},
	}

	addServiceFlags(cmd)
	addAuthFlags(cmd)

	cmd.Flags().BoolP(flagFollow, "f", false, "Specify if the logs should be streamed. Defaults to false")
	cmd.Flags().Int64P(flagTail, "t", -1, "The number of lines from the end of the logs to show. Defaults to -1")
	cmd.Flags().StringP(flagOutput, "o", outputText, "Output format text|json. Defaults to text")
	if err := addNoChainFlag(cmd); err != nil {
		panic(err)
	}

	if err := addProviderURLFlag(cmd); err != nil {
		panic(err)
	}

	return cmd
}

func doLeaseLogs(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl := cli.MustClientFromContext(ctx)
	cctx := cl.ClientContext()

	qclient, err := setupChainClient(ctx, cctx, cmd.Flags())
	if err != nil {
		return err
	}

	leases, err := leasesForDeployment(ctx, cctx, cmd.Flags(), qclient)
	if err != nil {
		return err
	}

	svcs, err := cmd.Flags().GetString(FlagService)
	if err != nil {
		return err
	}

	outputFormat, err := cmd.Flags().GetString(flagOutput)
	if err != nil {
		return err
	}

	if outputFormat != outputText && outputFormat != outputJSON {
		return fmt.Errorf("invalid output format %s. expected text|json", outputFormat)
	}

	follow, err := cmd.Flags().GetBool(flagFollow)
	if err != nil {
		return err
	}

	tailLines, err := cmd.Flags().GetInt64(flagTail)
	if err != nil {
		return err
	}

	if tailLines < -1 {
		return fmt.Errorf("tail flag supplied with invalid value. must be >= -1")
	}

	type streamResult struct {
		lid    mtypes.LeaseID
		error  error
		stream *apclient.ServiceLogs
	}

	streams := make([]streamResult, 0, len(leases))

	for _, lid := range leases {
		stream := streamResult{lid: lid}

		gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), qclient, true)
		if err == nil {
			stream.stream, stream.error = gclient.LeaseLogs(ctx, lid, svcs, follow, tailLines)
		} else {
			stream.error = err
		}

		streams = append(streams, stream)
	}

	var wgStreams sync.WaitGroup

	type logEntry struct {
		apclient.ServiceLogMessage `json:",inline"`
		Lid                        mtypes.LeaseID `json:"lease_id"`
	}

	outch := make(chan logEntry)

	printFn := func(evt logEntry) {
		fmt.Printf("[%s][%s] %s\n", evt.Lid, evt.Name, evt.Message)
	}

	if outputFormat == "json" {
		printFn = func(evt logEntry) {
			_ = cli.PrintJSON(cctx, evt)
		}
	}

	go func() {
		for evt := range outch {
			printFn(evt)
		}
	}()

	for _, stream := range streams {
		if stream.error != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "error getting lease logs: %v\n", stream.error)
			continue
		}

		wgStreams.Add(1)
		go func(stream streamResult) {
			defer wgStreams.Done()

			for res := range stream.stream.Stream {
				outch <- logEntry{
					ServiceLogMessage: res,
					Lid:               stream.lid,
				}
			}
		}(stream)
	}

	wgStreams.Wait()
	close(outch)

	return nil
}
