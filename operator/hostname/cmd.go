package hostname

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/common"
	"github.com/akash-network/provider/tools/fromctx"
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "hostname",
		Short:        "kubernetes operator interfacing with k8s nginx ingress",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			group := fromctx.MustErrGroupFromCtx(ctx)

			ns := viper.GetString(providerflags.FlagK8sManifestNS)

			config := common.GetOperatorConfigFromViper()

			logger := common.OpenLogger().With("op", "hostname")

			restPort, err := common.DetectPort(ctx, cmd.Flags(), common.FlagRESTPort, "operator-hostname", "api")
			if err != nil {
				return err
			}

			restAddr := fmt.Sprintf(":%d", restPort)

			op, err := newHostnameOperator(ctx, logger, ns, config, common.IgnoreListConfigFromViper())
			if err != nil {
				return err
			}

			router := op.server.GetRouter()

			// fixme ovrclk/engineering#609
			// nolint: gosec
			srv := http.Server{Addr: restAddr, Handler: router}

			group.Go(func() error {
				logger.Info("HTTP listening", "address", restAddr)
				return srv.ListenAndServe()
			})

			group.Go(func() error {
				<-ctx.Done()

				_ = srv.Close()

				return ctx.Err()
			})

			group.Go(op.run)

			fromctx.MustStartupChFromCtx(ctx) <- struct{}{}

			err = group.Wait()

			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				return err
			}

			return nil
		},
	}

	common.AddOperatorFlags(cmd)
	common.AddIgnoreListFlags(cmd)

	return cmd
}
