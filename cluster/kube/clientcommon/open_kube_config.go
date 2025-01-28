package clientcommon

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tendermint/tendermint/libs/log"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"

	providerflags "github.com/akash-network/provider/cmd/provider-services/cmd/flags"
	cmdutil "github.com/akash-network/provider/cmd/provider-services/cmd/util"
	"github.com/akash-network/provider/tools/fromctx"
)

func OpenKubeConfig(cfgPath string, log log.Logger) (*rest.Config, error) {
	// Always bypass the default rate limiting
	rateLimiter := flowcontrol.NewFakeAlwaysRateLimiter()
	// rateLimiter := flowcontrol.NewTokenBucketRateLimiter(1000, 3000)

	// if cfgPath contains value it is either set to default value $HOME/.kube/config
	// or explicitly by env/flag AP_KUBECONFIG/--kubeconfig
	if cfgPath != "" {
		cfgPath = os.ExpandEnv(cfgPath)

		if _, err := os.Stat(cfgPath); err == nil {
			log.Info("using kube config file", "path", cfgPath)
			cfg, err := clientcmd.BuildConfigFromFlags("", cfgPath)
			if err != nil {
				return cfg, fmt.Errorf("%w: error building kubernetes config", err)
			}
			cfg.RateLimiter = rateLimiter
			return cfg, err
		}
	}

	log.Info("using in cluster kube config")
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return cfg, fmt.Errorf("%w: error building kubernetes config", err)
	}
	cfg.RateLimiter = rateLimiter

	return cfg, err
}

func SetKubeConfigToCmd(c *cobra.Command) error {
	configPath, _ := c.Flags().GetString(providerflags.FlagKubeConfig)

	config, err := OpenKubeConfig(configPath, cmdutil.OpenLogger().With("cmp", "provider"))
	if err != nil {
		return err
	}

	fromctx.CmdSetContextValue(c, fromctx.CtxKeyKubeConfig, config)

	return nil
}
