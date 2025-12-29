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
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			group := fromctx.MustErrGroupFromCtx(ctx)

			ns := viper.GetString(providerflags.FlagK8sManifestNS)

			config := common.GetOperatorConfigFromViper()

			logger := common.OpenLogger().With("op", "hostname")

			ctx = withGatewayApi(ctx)

			logger.Info("hostname operator configuration",
				"ingress-mode", fromctx.IngressModeFromCtx(ctx),
				"gateway-name", fromctx.GatewayNameFromCtx(ctx),
				"gateway-namespace", fromctx.GatewayNamespaceFromCtx(ctx))

			restPort, err := common.DetectPort(ctx, cmd.Flags(), common.FlagRESTPort, "operator-hostname", "rest")
			if err != nil {
				return err
			}

			listenAddress := viper.GetString(common.FlagRESTAddress)
			restAddr := fmt.Sprintf("%s:%d", listenAddress, restPort)

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

	addGatewayApiFlags(cmd)

	return cmd
}

func addGatewayApiFlags(cmd *cobra.Command) {
	cmd.Flags().String("ingress-mode", "ingress", "Ingress mode: 'ingress' for NGINX Ingress (default) or 'gateway-api' for Gateway API")
	if err := viper.BindPFlag("ingress-mode", cmd.Flags().Lookup("ingress-mode")); err != nil {
		panic(err)
	}

	cmd.Flags().String("gateway-name", "akash-gateway", "Gateway name when using gateway-api mode")
	if err := viper.BindPFlag("gateway-name", cmd.Flags().Lookup("gateway-name")); err != nil {
		panic(err)
	}

	cmd.Flags().String("gateway-namespace", "akash-gateway", "Gateway namespace when using gateway-api mode")
	if err := viper.BindPFlag("gateway-namespace", cmd.Flags().Lookup("gateway-namespace")); err != nil {
		panic(err)
	}
}

func withGatewayApi(ctx context.Context) context.Context {

	ingressMode := viper.GetString("ingress-mode")
	gatewayName := viper.GetString("gateway-name")
	gatewayNamespace := viper.GetString("gateway-namespace")

	ctx = context.WithValue(ctx, fromctx.CtxKeyIngressMode, ingressMode)
	ctx = context.WithValue(ctx, fromctx.CtxKeyGatewayName, gatewayName)
	ctx = context.WithValue(ctx, fromctx.CtxKeyGatewayNamespace, gatewayNamespace)

	return ctx
}
