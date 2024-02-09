package operator

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/troian/pubsub"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/net/context"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/kubernetes"

	"github.com/akash-network/provider/cluster/kube/clientcommon"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/operator/common"
	"github.com/akash-network/provider/operator/hostname"
	"github.com/akash-network/provider/operator/inventory"
	"github.com/akash-network/provider/operator/ip"
	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	"github.com/akash-network/provider/tools/fromctx"
)

func OperatorsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "operator",
		Short:        "kubernetes operators control",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			zconf := zap.NewDevelopmentConfig()
			zconf.DisableCaller = true
			zconf.DisableStacktrace = true
			zconf.EncoderConfig.EncodeTime = func(time.Time, zapcore.PrimitiveArrayEncoder) {}

			zapLog, _ := zconf.Build()

			group, ctx := errgroup.WithContext(cmd.Context())

			cmd.SetContext(logr.NewContext(ctx, zapr.NewLogger(zapLog)))

			kubecfg, err := fromctx.KubeConfigFromCtx(cmd.Context())
			if err != nil {
				if err := clientcommon.SetKubeConfigToCmd(cmd); err != nil {
					return err
				}

				kubecfg = fromctx.MustKubeConfigFromCtx(cmd.Context())
			}

			if val := ctx.Value(fromctx.CtxKeyKubeClientSet); val == nil {
				kc, err := kubernetes.NewForConfig(kubecfg)
				if err != nil {
					return err
				}
				fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kc))
			}

			if val := ctx.Value(fromctx.CtxKeyAkashClientSet); val == nil {
				ac, err := akashclientset.NewForConfig(kubecfg)
				if err != nil {
					return err
				}

				fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(ac))
			}

			startupch := make(chan struct{}, 1)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyStartupCh, (chan<- struct{})(startupch))

			pctx, pcancel := context.WithCancel(context.Background())

			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyErrGroup, group)
			fromctx.CmdSetContextValue(cmd, fromctx.CtxKeyPubSub, pubsub.New(pctx, 1000))

			go func() {
				defer pcancel()

				select {
				case <-ctx.Done():
					return
				case <-startupch:
				}

				_ = group.Wait()
			}()

			return nil
		},
	}

	cmd.PersistentFlags().Uint16(common.FlagRESTPort, 8080, "REST port")
	if err := viper.BindPFlag(common.FlagRESTPort, cmd.PersistentFlags().Lookup(common.FlagRESTPort)); err != nil {
		panic(err)
	}

	cmd.PersistentFlags().Uint16(common.FlagGRPCPort, 8081, "REST port")
	if err := viper.BindPFlag(common.FlagGRPCPort, cmd.PersistentFlags().Lookup(common.FlagGRPCPort)); err != nil {
		panic(err)
	}

	cmd.Flags().String(common.FlagRESTAddress, "0.0.0.0", "listen address for REST server")
	if err := viper.BindPFlag(common.FlagRESTAddress, cmd.Flags().Lookup(common.FlagRESTAddress)); err != nil {
		panic(err)
	}

	err := providerflags.AddKubeConfigPathFlag(cmd)
	if err != nil {
		panic(err)
	}

	cmd.AddCommand(inventory.Cmd())
	cmd.AddCommand(ip.Cmd())
	cmd.AddCommand(hostname.Cmd())

	return cmd
}

func ToolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "tools",
		Short:        "debug tools",
		SilenceUsage: true,
	}

	cmd.AddCommand(cmdPsutil())

	return cmd
}
