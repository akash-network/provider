package flags

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	KubeConfigDefaultPath = "$HOME/.kube/config"
)

func AddKubeConfigPathFlag(cmd *cobra.Command) error {
	cmd.Flags().String(FlagKubeConfig, KubeConfigDefaultPath, "kubernetes configuration file path")
	return viper.BindPFlag(FlagKubeConfig, cmd.Flags().Lookup(FlagKubeConfig))
}
