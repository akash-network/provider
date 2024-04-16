package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	"github.com/akash-network/node/sdl"
	cutils "github.com/akash-network/node/x/cert/utils"

	aclient "github.com/akash-network/provider/client"
	gwgrpc "github.com/akash-network/provider/gateway/grpc"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

var errSubmitManifestFailed = errors.New("submit manifest to some providers has been failed")

// SendManifestCmd looks up the Providers blockchain information,
// and POSTs the SDL file to the Gateway address.
func SendManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "send-manifest <sdl-path>",
		Args:         cobra.ExactArgs(1),
		Short:        "Submit manifest to provider(s)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSendManifest(cmd, args[0])
		},
	}

	addManifestFlags(cmd)

	cmd.Flags().StringP(flagOutput, "o", outputText, "output format text|json|yaml. default text")

	return cmd
}

func doSendManifest(cmd *cobra.Command, sdlpath string) error {
	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	sdl, err := sdl.ReadFile(sdlpath)
	if err != nil {
		return err
	}

	mani, err := sdl.Manifest()
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

	// owner address in FlagFrom has already been validated thus save to just pull its value as string
	leases, err := leasesForDeployment(cmd.Context(), cl, cmd.Flags(), dtypes.DeploymentID{
		Owner: cctx.GetFromAddress().String(),
		DSeq:  dseq,
	})
	if err != nil {
		return markRPCServerError(err)
	}

	type result struct {
		Provider     sdk.Address `json:"provider" yaml:"provider"`
		Status       string      `json:"status" yaml:"status"`
		Error        string      `json:"error,omitempty" yaml:"error,omitempty"`
		ErrorMessage string      `json:"errorMessage,omitempty" yaml:"errorMessage,omitempty"`
	}

	var (
		results      = make([]result, len(leases))
		submitFailed = false
	)

	for i, lid := range leases {
		err = func() error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			provAddr, _ := sdk.AccAddressFromBech32(lid.Provider)
			prov, err := cl.Provider(ctx, &ptypes.QueryProviderRequest{Owner: provAddr.String()})
			if err != nil {
				return fmt.Errorf("query client provider: %w", err)
			}

			hostURIgRPC, err := grpcURI(prov.GetProvider().HostURI)
			if err != nil {
				return fmt.Errorf("grpc uri: %w", err)
			}

			res := result{
				Provider: provAddr,
				Status:   "PASS",
			}

			c, err := gwgrpc.NewClient(ctx, hostURIgRPC, cert, cl)
			if err == nil {
				defer c.Close()

				if _, err = c.SendManifest(ctx, &leasev1.SendManifestRequest{
					LeaseId:  lid,
					Manifest: mani,
				}); err != nil {
					res.Error = err.Error()
					res.Status = "FAIL"
					submitFailed = true
				}
			} else {
				gclient, err := gwrest.NewClient(cl, provAddr, []tls.Certificate{cert})
				if err != nil {
					return fmt.Errorf("gwrest new client: %w", err)
				}

				err = gclient.SubmitManifest(cmd.Context(), dseq, mani)
				if err != nil {
					res.Error = err.Error()
					if e, valid := err.(gwrest.ClientResponseError); valid {
						res.ErrorMessage = e.Message
					}
					res.Status = "FAIL"
					submitFailed = true
				}
			}

			results[i] = res

			return nil
		}()
		if err != nil {
			return err
		}
	}

	buf := &bytes.Buffer{}

	switch cmd.Flag(flagOutput).Value.String() {
	case outputText:
		for _, res := range results {
			_, _ = fmt.Fprintf(buf, "provider: %s\n\tstatus:       %s\n", res.Provider, res.Status)
			if res.Error != "" {
				_, _ = fmt.Fprintf(buf, "\terror:        %v\n", res.Error)
			}
			if res.ErrorMessage != "" {
				_, _ = fmt.Fprintf(buf, "\terrorMessage: %v\n", res.ErrorMessage)
			}
		}
	case outputJSON:
		err = json.NewEncoder(buf).Encode(results)
	case outputYAML:
		err = yaml.NewEncoder(buf).Encode(results)
	}

	if err != nil {
		return err
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), buf.String())
	if err != nil {
		return err
	}

	if submitFailed {
		return errSubmitManifestFailed
	}

	return nil
}

