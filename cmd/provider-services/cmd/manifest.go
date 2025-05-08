package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	"github.com/akash-network/node/sdl"

	aclient "github.com/akash-network/provider/client"
	gwrest "github.com/akash-network/provider/gateway/rest"
)

var (
	errSubmitManifestFailed = errors.New("submit manifest to some providers has been failed")
)

func ManifestCmds() []*cobra.Command {
	return []*cobra.Command{
		SendManifestCmd(),
		GetManifestCmd(),
	}
}

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

// GetManifestCmd reads the current manifest from the provider
func GetManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "get-manifest",
		Args:         cobra.ExactArgs(0),
		Short:        "Read manifest from provider",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cctx, err := sdkclient.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			ctx := cmd.Context()

			cl, err := aclient.DiscoverQueryClient(ctx, cctx)
			if err != nil {
				return err
			}

			lid, err := leaseIDFromFlags(cmd.Flags(), cctx.GetFromAddress().String())
			if err != nil {
				return err
			}

			prov, _ := sdk.AccAddressFromBech32(lid.Provider)
			gclient, err := gwrest.NewClient(ctx, cl, prov, gwrest.WithJWTSigner(ajwt.NewSigner(cctx.Keyring, cctx.FromAddress)))
			if err != nil {
				return err
			}

			mani, err := gclient.GetManifest(ctx, lid)
			if err != nil {
				return err
			}

			buf := &bytes.Buffer{}

			switch cmd.Flag(flagOutput).Value.String() {
			case outputJSON:
				err = json.NewEncoder(buf).Encode(mani)
			case outputYAML:
				err = yaml.NewEncoder(buf).Encode(mani)
			}

			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), buf.String())

			if err != nil {
				return err
			}

			return nil
		},
	}

	addLeaseFlags(cmd)

	cmd.Flags().StringP(flagOutput, "o", outputYAML, "output format json|yaml. default yaml")

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

	dseq, err := dseqFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	// the owner address in FlagFrom has already been validated thus save to just pull its value as string
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

	results := make([]result, len(leases))

	submitFailed := false

	for i, lid := range leases {
		prov, _ := sdk.AccAddressFromBech32(lid.Provider)
		gclient, err := gwrest.NewClient(ctx, cl, prov, gwrest.WithJWTSigner(ajwt.NewSigner(cctx.Keyring, cctx.FromAddress)))
		if err != nil {
			return err
		}

		err = gclient.SubmitManifest(ctx, dseq, mani)
		res := result{
			Provider: prov,
			Status:   "PASS",
		}
		if err != nil {
			res.Error = err.Error()
			if e, valid := err.(gwrest.ClientResponseError); valid {
				res.ErrorMessage = e.Message
			}
			res.Status = "FAIL"
			submitFailed = true
		}

		results[i] = res
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
