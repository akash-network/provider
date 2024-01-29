package flags

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	KubeConfigDefaultPath = "$HOME/.kube/config"
)

func AddKubeConfigPathFlag(cmd *cobra.Command) error {
	cmd.PersistentFlags().String(FlagKubeConfig, "$HOME/.kube/config", "kubernetes configuration file path")
	if err := viper.BindEnv(FlagKubeConfig, "KUBECONFIG"); err != nil {
		return err
	}

	return viper.BindPFlag(FlagKubeConfig, cmd.PersistentFlags().Lookup(FlagKubeConfig))
}
