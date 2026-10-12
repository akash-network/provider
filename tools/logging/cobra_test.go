package logging

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
}

func clearLogEnv(t *testing.T) {
	t.Helper()
	for _, prefix := range []string{"AP_", "AKASH_"} {
		for _, name := range []string{"LOG_LEVEL", "LOG_FORMAT", "LOG_COLOR", "LOG_TIMESTAMP", "TRACE"} {
			t.Setenv(prefix+name, "")
		}
	}
}

// setupCmd returns a command carrying the logging flags whose RunE runs Setup
// and stores the resulting config in *got.
func setupCmd(got *Config) *cobra.Command {
	cmd := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := Setup(cmd); err != nil {
				return err
			}
			cfg, ok := ConfigFromCtx(cmd.Context())
			if !ok {
				return context.Canceled
			}
			*got = cfg
			return nil
		},
	}
	AddFlags(cmd.PersistentFlags())
	cmd.PersistentFlags().Bool(FlagTrace, false, "")

	return cmd
}

func TestAddFlagsDefaults(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	AddFlags(cmd.PersistentFlags())

	fs := cmd.PersistentFlags()
	require.Equal(t, DefaultLevel, fs.Lookup(FlagLogLevel).DefValue)
	require.Equal(t, DefaultFormat, fs.Lookup(FlagLogFormat).DefValue)
	require.Equal(t, "true", fs.Lookup(FlagLogColor).DefValue)
	require.Equal(t, DefaultTimestamp, fs.Lookup(FlagLogTimestamp).DefValue)
}

func TestSetupBindsFlags(t *testing.T) {
	resetViper(t)
	clearLogEnv(t)

	var got Config
	cmd := setupCmd(&got)
	cmd.SetArgs([]string{"--log_format", "json", "--log_color=true", "--trace"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))

	require.Equal(t, FormatJSON, got.Format)
	require.False(t, got.Color)
	require.True(t, got.Trace)
}

func TestSetupDefaults(t *testing.T) {
	resetViper(t)
	clearLogEnv(t)

	var got Config
	cmd := setupCmd(&got)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	require.Equal(t, DefaultConfig(), got)
}

func TestSetupEnv(t *testing.T) {
	resetViper(t)
	clearLogEnv(t)
	t.Setenv("AP_LOG_TIMESTAMP", "kitchen")
	viper.SetEnvPrefix("AP")
	viper.AutomaticEnv()

	var got Config
	cmd := setupCmd(&got)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	require.Equal(t, TimestampKitchen, got.Timestamp)
}

func TestSetupInvalid(t *testing.T) {
	resetViper(t)
	clearLogEnv(t)

	var got Config
	cmd := setupCmd(&got)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--log_format", "text"})
	require.ErrorContains(t, cmd.ExecuteContext(context.Background()), "plain|json")
}

func TestSetupIdempotent(t *testing.T) {
	resetViper(t)
	clearLogEnv(t)

	cmd := &cobra.Command{Use: "x"}
	AddFlags(cmd.PersistentFlags())
	cmd.SetContext(context.Background())

	l1, err := Setup(cmd)
	require.NoError(t, err)
	l2, err := Setup(cmd)
	require.NoError(t, err)
	require.Same(t, l1, l2)
}

func TestNormalizeTimestampForUpstream(t *testing.T) {
	cases := map[string]struct {
		args []string
		env  map[string]string
	}{
		"flag":      {args: []string{"--log_timestamp", "OFF"}},
		"ap env":    {env: map[string]string{"AP_LOG_TIMESTAMP": "off"}},
		"akash env": {env: map[string]string{"AKASH_LOG_TIMESTAMP": "off"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resetViper(t)
			clearLogEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			args := tc.args

			var got string
			cmd := &cobra.Command{
				Use: "x",
				RunE: func(cmd *cobra.Command, _ []string) error {
					if err := NormalizeTimestampForUpstream(cmd, "AP", "AKASH"); err != nil {
						return err
					}
					got, _ = cmd.Flags().GetString(FlagLogTimestamp)
					return nil
				},
			}
			AddFlags(cmd.PersistentFlags())
			cmd.SetArgs(args)
			require.NoError(t, cmd.ExecuteContext(context.Background()))
			require.Equal(t, "", got)
		})
	}

	t.Run("other values untouched", func(t *testing.T) {
		resetViper(t)
		clearLogEnv(t)

		var got string
		cmd := &cobra.Command{
			Use: "x",
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := NormalizeTimestampForUpstream(cmd, "AP", "AKASH"); err != nil {
					return err
				}
				got, _ = cmd.Flags().GetString(FlagLogTimestamp)
				return nil
			},
		}
		AddFlags(cmd.PersistentFlags())
		cmd.SetArgs([]string{"--log_timestamp", "kitchen"})
		require.NoError(t, cmd.ExecuteContext(context.Background()))
		require.Equal(t, "kitchen", got)
	})
}
