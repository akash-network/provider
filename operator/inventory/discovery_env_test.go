package inventory

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/akash-network/provider/tools/logging"
)

func TestDiscoveryPodEnvPropagatesLogging(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	cmd := &cobra.Command{Use: "x"}
	logging.AddFlags(cmd.Flags())
	require.NoError(t, cmd.Flags().Set(logging.FlagLogFormat, "json"))
	require.NoError(t, cmd.Flags().Set(logging.FlagLogLevel, "psutil:debug,*:warn"))
	cmd.SetContext(context.Background())
	_, err := logging.Setup(cmd)
	require.NoError(t, err)

	require.Equal(t, []corev1.EnvVar{
		{Name: "PCIDB_ENABLE_NETWORK_FETCH", Value: "1"},
		{Name: "AP_LOG_LEVEL", Value: "psutil:debug,*:warn"},
		{Name: "AP_LOG_FORMAT", Value: "json"},
		{Name: "AP_LOG_COLOR", Value: "false"},
		{Name: "AP_LOG_TIMESTAMP", Value: "rfc3339"},
		{Name: "AP_TRACE", Value: "false"},
	}, discoveryPodEnv(cmd.Context()))
}

func TestDiscoveryPodEnvDefaultsWithoutLoggingConfig(t *testing.T) {
	env := discoveryPodEnv(context.Background())
	require.Contains(t, env, corev1.EnvVar{Name: "AP_LOG_FORMAT", Value: "plain"})
	require.Contains(t, env, corev1.EnvVar{Name: "PCIDB_ENABLE_NETWORK_FETCH", Value: "1"})
}
