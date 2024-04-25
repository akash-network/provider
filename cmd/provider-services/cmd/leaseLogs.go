package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/akash-network/akash-api/go/node/client/v1beta2"
	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	cmdcommon "github.com/akash-network/node/cmd/common"
	cutils "github.com/akash-network/node/x/cert/utils"

	aclient "github.com/akash-network/provider/client"
	gwgrpc "github.com/akash-network/provider/gateway/grpc"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

func leaseLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-logs",
		Short:        "get lease logs",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doLeaseLogs(cmd)
		},
	}

	addServiceFlags(cmd)

	cmd.Flags().BoolP(flagFollow, "f", false, "Specify if the logs should be streamed. Defaults to false")
	cmd.Flags().Int64P(flagTail, "t", -1, "The number of lines from the end of the logs to show. Defaults to -1")
	cmd.Flags().StringP(flagOutput, "o", outputText, "Output format text|json. Defaults to text")

	return cmd
}

func doLeaseLogs(cmd *cobra.Command) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	cert, err := cutils.LoadAndQueryCertificateForAccount(cmd.Context(), cctx, nil)
	if err != nil {
		return markRPCServerError(err)
	}

	dseq, err := dseqFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	leases, err := leasesForDeployment(cmd.Context(), cl, cmd.Flags(), dtypes.DeploymentID{
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

	outputFormat, err := cmd.Flags().GetString(flagOutput)
	if err != nil {
		return err
	}

	if outputFormat != outputText && outputFormat != outputJSON {
		return errors.Errorf("invalid output format %s. expected text|json", outputFormat)
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
		return errors.Errorf("tail flag supplied with invalid value. must be >= -1")
	}

	g := leaseLogGetter{
		cert:         cert,
		cl:           cl,
		svcs:         svcs,
		follow:       follow,
		tailLines:    tailLines,
		outputFormat: outputFormat,
		cctx:         cctx,
	}

	if err = g.run(ctx, leases); err != nil {
		return fmt.Errorf("getting logs: %w", err)
	}

	return nil
}

type leaseLogGetter struct {
	cert         tls.Certificate
	cl           v1beta2.QueryClient
	svcs         string
	follow       bool
	tailLines    int64
	outputFormat string
	cctx         sdkclient.Context
}

func (g leaseLogGetter) run(ctx context.Context, leases []mtypes.LeaseID) error {
	var (
		restLeases = make([]mtypes.LeaseID, 0, len(leases))
		grpcLeases = make(map[mtypes.LeaseID]*gwgrpc.Client)
	)

	for _, lid := range leases {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		provAddr, _ := sdk.AccAddressFromBech32(lid.Provider)
		prov, err := g.cl.Provider(ctx, &ptypes.QueryProviderRequest{Owner: provAddr.String()})
		if err != nil {
			return fmt.Errorf("query client provider: %w", err)
		}

		hostURIgRPC, err := grpcURI(prov.GetProvider().HostURI)
		if err != nil {
			return fmt.Errorf("grpc uri: %w", err)
		}

		client, err := gwgrpc.NewClient(ctx, hostURIgRPC, g.cert, g.cl)
		if err == nil {
			grpcLeases[lid] = client
		} else {
			restLeases = append(restLeases, lid)
		}
	}

	g.grpc(ctx, grpcLeases)
	g.rest(ctx, restLeases)

	return nil
}

func (g leaseLogGetter) grpc(ctx context.Context, leases map[mtypes.LeaseID]*gwgrpc.Client) {
}

func (g leaseLogGetter) rest(ctx context.Context, leases []mtypes.LeaseID) {
	type result struct {
		lid    mtypes.LeaseID
		error  error
		stream *gwrest.ServiceLogs
	}

	streams := make([]result, 0, len(leases))

	for _, lid := range leases {
		stream := result{lid: lid}
		prov, _ := sdk.AccAddressFromBech32(lid.Provider)
		gclient, err := gwrest.NewClient(g.cl, prov, []tls.Certificate{g.cert})
		if err == nil {
			stream.stream, stream.error = gclient.LeaseLogs(ctx, lid, g.svcs, g.follow, g.tailLines)
		} else {
			stream.error = err
		}

		streams = append(streams, stream)
	}

	var wgStreams sync.WaitGroup

	type logEntry struct {
		gwrest.ServiceLogMessage `json:",inline"`
		Lid                      mtypes.LeaseID `json:"lease_id"`
	}

	outch := make(chan logEntry)

	printFn := func(evt logEntry) {
		fmt.Printf("[%s][%s] %s\n", evt.Lid, evt.Name, evt.Message)
	}

	if g.outputFormat == "json" {
		printFn = func(evt logEntry) {
			_ = cmdcommon.PrintJSON(g.cctx, evt)
		}
	}

	go func() {
		for evt := range outch {
			printFn(evt)
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
					ServiceLogMessage: res,
					Lid:               stream.lid,
				}
			}
		}(stream)
	}

	wgStreams.Wait()
	close(outch)
}
