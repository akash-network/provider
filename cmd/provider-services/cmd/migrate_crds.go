package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/cmd/provider-services/cmd/util"
	"github.com/akash-network/provider/migrations"
	"github.com/akash-network/provider/tools/fromctx"
)

func MigrateCRDsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "crds",
		Short:        "apply akash CRDs to the cluster",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := clientcommon.SetKubeConfigToCmd(cmd); err != nil {
				return err
			}

			logger := util.OpenLogger()
			ctx := ctxlog.WithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			kubecfg := fromctx.MustKubeConfigFromCtx(cmd.Context())

			dc, err := dynamic.NewForConfig(kubecfg)
			if err != nil {
				return fmt.Errorf("creating dynamic client: %w", err)
			}

			names, err := migrations.ApplyCRDs(cmd.Context(), dc)
			if err != nil {
				return fmt.Errorf("applying CRDs: %w", err)
			}

			logger := ctxlog.LogcFromCtx(cmd.Context())
			logger.Info("applied CRDs", "names", names)

			return nil
		},
	}

	if err := providerflags.AddKubeConfigPathFlag(cmd); err != nil {
		panic(err.Error())
	}

	return cmd
}
