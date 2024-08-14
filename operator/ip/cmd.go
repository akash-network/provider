package ip

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/spf13/viper"

	clusterClient "github.com/akash-network/provider/cluster/kube"
	"github.com/akash-network/provider/cluster/kube/operators/clients/metallb"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/common"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	flagMetalLbPoolName = "metal-lb-pool"
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "ip",
		Short:        "kubernetes operator interfacing with Metal LB",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ns := viper.GetString(providerflags.FlagK8sManifestNS)
			poolName := viper.GetString(flagMetalLbPoolName)
			logger := common.OpenLogger().With("operator", "ip")

			ctx := cmd.Context()

			opcfg := common.GetOperatorConfigFromViper()
			_, err := sdk.AccAddressFromBech32(opcfg.ProviderAddress)
			if err != nil {
				return fmt.Errorf("%w: provider address must valid bech32", err)
			}

			client, err := clusterClient.NewClient(cmd.Context(), logger, ns)
			if err != nil {
				return err
			}

			metalLbEndpoint, err := providerflags.GetServiceEndpointFlagValue(logger, serviceMetalLb)
			if err != nil {
				return err
			}

			mllbc, err := metallb.NewClient(ctx, logger, poolName, metalLbEndpoint)
			if err != nil {
				return err
			}

			restPort, err := common.DetectPort(ctx, cmd.Flags(), common.FlagRESTPort, "operator-ip", "rest")
			if err != nil {
				return err
			}

			restAddr := fmt.Sprintf(":%d", restPort)

			group := fromctx.MustErrGroupFromCtx(ctx)

			logger.Info("clients", "kube", client, "metallb", mllbc)

			op, err := newIPOperator(ctx, logger, ns, opcfg, common.IgnoreListConfigFromViper(), mllbc)
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

			group.Go(func() error {
				return op.run(ctx)
			})

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
	common.AddProviderFlag(cmd)

	if err := providerflags.AddServiceEndpointFlag(cmd, serviceMetalLb); err != nil {
		return nil
	}

	cmd.Flags().String(flagMetalLbPoolName, "", "metal LB ip address pool to use")
	err := viper.BindPFlag(flagMetalLbPoolName, cmd.Flags().Lookup(flagMetalLbPoolName))
	if err != nil {
		panic(err)
	}

	return cmd
}
