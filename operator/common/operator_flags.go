package common

import (
	"time"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kutil "github.com/akash-network/provider/cluster/kube/util"
	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	flagProviderAddress = "provider"
	FlagRESTPort        = "rest-port"
	FlagRESTAddress     = "rest-address"
	FlagGRPCPort        = "grpc-port"
	FlagPod             = "pod-name"
	FlagNamespace       = "pod-namespace"
)

func AddOperatorFlags(cmd *cobra.Command) {
	cmd.Flags().String(providerflags.FlagK8sManifestNS, "lease", "Cluster manifest namespace")
	if err := viper.BindPFlag(providerflags.FlagK8sManifestNS, cmd.Flags().Lookup(providerflags.FlagK8sManifestNS)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagPruneInterval, 10*time.Minute, "data pruning interval")
	if err := viper.BindPFlag(providerflags.FlagPruneInterval, cmd.Flags().Lookup(providerflags.FlagPruneInterval)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagWebRefreshInterval, 5*time.Second, "web data refresh interval")
	if err := viper.BindPFlag(providerflags.FlagWebRefreshInterval, cmd.Flags().Lookup(providerflags.FlagWebRefreshInterval)); err != nil {
		panic(err)
	}

	cmd.Flags().Duration(providerflags.FlagRetryDelay, 3*time.Second, "retry delay")
	if err := viper.BindPFlag(providerflags.FlagRetryDelay, cmd.Flags().Lookup(providerflags.FlagRetryDelay)); err != nil {
		panic(err)
	}
}

func AddProviderFlag(cmd *cobra.Command) {
	cmd.Flags().String(flagProviderAddress, "", "address of associated provider in bech32")
	if err := viper.BindPFlag(flagProviderAddress, cmd.Flags().Lookup(flagProviderAddress)); err != nil {
		panic(err)
	}
}

func DetectPort(ctx context.Context, flags *flag.FlagSet, flag string, container, portName string) (int, error) {
	var port uint16

	log := fromctx.LogrFromCtx(ctx)

	if flags.Changed(flag) || !kutil.IsInsideKubernetes() {
		var err error
		port, err = flags.GetUint16(flag)
		if err != nil {
			return 0, err
		}

		return int(port), nil
	}

	if kutil.IsInsideKubernetes() {
		podName := viper.GetString(FlagPod)
		namespace := viper.GetString(FlagNamespace)

		kc, err := fromctx.KubeClientFromCtx(ctx)
		if err != nil {
			return 0, err
		}

		pod, err := kc.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return 0, err
		}

	loop:
		for _, scontainer := range pod.Spec.Containers {
			if scontainer.Name == container {
				for _, cport := range scontainer.Ports {
					if cport.Name == portName {
						port = uint16(cport.ContainerPort)
						break loop
					}
				}
			}
		}
	}

	if port == 0 {
		log.Info("unable to detect rest port from pod. falling back to default \"\". default might differ from what service expects!")
		var err error
		port, err = flags.GetUint16(flag)
		if err != nil {
			return 0, err
		}
	}

	return int(port), nil
}
