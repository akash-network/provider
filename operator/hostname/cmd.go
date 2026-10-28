package hostname

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"pkg.akt.dev/go/util/ctxlog"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/cluster/kube/gateway"
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

			logger := ctxlog.LogcFromCtx(ctx).With("module", "operator-hostname")

			ctx, err := withGatewayApi(ctx)
			if err != nil {
				return err
			}

			gwCfg := fromctx.MustGatewayConfigFromCtx(ctx)
			logger.Info("hostname operator configuration",
				"gateway_name", gwCfg.Name,
				"gateway_namespace", gwCfg.Namespace,
				"gateway_provider", gwCfg.Provider)

			restPort, err := common.DetectPort(ctx, logger, cmd.Flags(), common.FlagRESTPort, "operator-hostname", "rest")
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
				logger.Info("HTTP listening", "listen_addr", restAddr)
				return common.ListenAndServe(&srv)
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
	cmd.Flags().String("ingress-mode", builder.IngressModeGateway, "Deprecated: only 'gateway-api' is supported; ingress-nginx support was removed")
	if err := viper.BindPFlag("ingress-mode", cmd.Flags().Lookup("ingress-mode")); err != nil {
		panic(err)
	}

	cmd.Flags().String("gateway-name", "akash-gateway", "Gateway that tenant HTTPRoutes attach to")
	if err := viper.BindPFlag("gateway-name", cmd.Flags().Lookup("gateway-name")); err != nil {
		panic(err)
	}

	cmd.Flags().String("gateway-namespace", "akash-gateway", "Namespace of the Gateway")
	if err := viper.BindPFlag("gateway-namespace", cmd.Flags().Lookup("gateway-namespace")); err != nil {
		panic(err)
	}

	cmd.Flags().String("gateway-provider", gateway.AutoProvider, "Gateway API implementation: 'auto' (detect from the Gateway's GatewayClass), 'envoy' or 'nginx'")
	if err := viper.BindPFlag("gateway-provider", cmd.Flags().Lookup("gateway-provider")); err != nil {
		panic(err)
	}

}

func withGatewayApi(ctx context.Context) (context.Context, error) {
	if err := builder.ValidateIngressMode(viper.GetString("ingress-mode")); err != nil {
		return nil, err
	}

	gatewayName := viper.GetString("gateway-name")
	gatewayNamespace := viper.GetString("gateway-namespace")
	gatewayProvider := viper.GetString("gateway-provider")

	if gatewayName == "" {
		return nil, fmt.Errorf("gateway-name is required")
	}
	if gatewayNamespace == "" {
		return nil, fmt.Errorf("gateway-namespace is required")
	}

	ctx = context.WithValue(ctx, fromctx.CtxKeyGatewayConfig, fromctx.GatewayConfig{
		Name:      gatewayName,
		Namespace: gatewayNamespace,
		Provider:  gatewayProvider,
	})

	return ctx, nil
}
