package ipoperator

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	providerflags "github.com/ovrclk/provider-services/cmd/provider-services/cmd/flags"
	"github.com/ovrclk/provider-services/operator/operatorcommon"
)

const (
	flagMetalLbPoolName = "metal-lb-pool"
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "ip-operator",
		Short:        "kubernetes operator interfacing with Metal LB",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return doIPOperator(cmd)
		},
	}
	operatorcommon.AddOperatorFlags(cmd, "0.0.0.0:8086")
	operatorcommon.AddIgnoreListFlags(cmd)
	operatorcommon.AddProviderFlag(cmd)

	if err := providerflags.AddServiceEndpointFlag(cmd, serviceMetalLb); err != nil {
		return nil
	}
	err := providerflags.AddKubeConfigPathFlag(cmd)
	if err != nil {
		panic(err)
	}

	cmd.Flags().String(flagMetalLbPoolName, "", "metal LB ip address pool to use")
	err = viper.BindPFlag(flagMetalLbPoolName, cmd.Flags().Lookup(flagMetalLbPoolName))
	if err != nil {
		panic(err)
	}

	return cmd
}
