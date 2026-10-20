package operator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	kfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	akashclientset "github.com/akash-network/provider/pkg/client/clientset/versioned"
	afake "github.com/akash-network/provider/pkg/client/clientset/versioned/fake"
	"github.com/akash-network/provider/tools/fromctx"
	"github.com/akash-network/provider/tools/logging"
)

var errProbe = errors.New("probe failed before startup")

const probeCmd = "probe"

// executeOperatorProbe mounts OperatorsCmd under a root carrying the logging
// flags (as the real root does) and runs a probe operator subcommand, with env
// set on top of an environment free of logging variables.
func executeOperatorProbe(t *testing.T, env map[string]string, runE func(cmd *cobra.Command, args []string) error, args ...string) error {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, prefix := range []string{"AP_", "AKASH_"} {
		for _, name := range []string{"LOG_LEVEL", "LOG_FORMAT", "LOG_COLOR", "LOG_TIMESTAMP", "TRACE"} {
			t.Setenv(prefix+name, "")
		}
	}
	for key, val := range env {
		t.Setenv(key, val)
	}

	root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true}
	logging.AddFlags(root.PersistentFlags())
	root.PersistentFlags().Bool(logging.FlagTrace, false, "")

	op := OperatorsCmd()
	op.AddCommand(withLifecycle(&cobra.Command{Use: probeCmd, RunE: runE}))
	root.AddCommand(op)

	ctx := context.Background()
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, &rest.Config{Host: "http://127.0.0.1:1"})
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kfake.NewSimpleClientset()))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(afake.NewSimpleClientset()))

	root.SetArgs(append([]string{"operator", probeCmd}, args...))

	return root.ExecuteContext(ctx)
}

func TestOperatorLoggingFlags(t *testing.T) {
	var got logging.Config
	var ok bool

	err := executeOperatorProbe(t, nil, func(cmd *cobra.Command, _ []string) error {
		got, ok = logging.ConfigFromCtx(cmd.Context())
		return nil
	}, "--log_format", "json", "--log_color=true", "--log_timestamp", "off")

	require.NoError(t, err)
	require.True(t, ok, "operator pre-run must set up logging")
	require.Equal(t, logging.FormatJSON, got.Format)
	require.False(t, got.Color)
	require.Equal(t, logging.TimestampOff, got.Timestamp)
}

// Operator subcommands do not run the root pre-run, which applies the AKASH_ fallback prefix.
// Their logging options must still be read from both prefixes, AP_ taking precedence.
func TestOperatorLoggingEnv(t *testing.T) {
	tests := map[string]struct {
		env           map[string]string
		wantFormat    string
		wantTimestamp string
	}{
		"AKASH_ prefix": {
			env:           map[string]string{"AKASH_LOG_FORMAT": "json", "AKASH_LOG_TIMESTAMP": "kitchen"},
			wantFormat:    "json",
			wantTimestamp: "kitchen",
		},
		"AP_ wins over AKASH_": {
			env:           map[string]string{"AP_LOG_TIMESTAMP": "kitchen", "AKASH_LOG_TIMESTAMP": "rfc3339", "AKASH_LOG_FORMAT": "json"},
			wantFormat:    "json",
			wantTimestamp: "kitchen",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var got logging.Config

			err := executeOperatorProbe(t, tc.env, func(cmd *cobra.Command, _ []string) error {
				got, _ = logging.ConfigFromCtx(cmd.Context())
				return nil
			})

			require.NoError(t, err)
			require.Equal(t, tc.wantFormat, got.Format)
			require.Equal(t, tc.wantTimestamp, got.Timestamp)
		})
	}
}

func TestOperatorRunEErrorCancelsGroup(t *testing.T) {
	var runCtx context.Context

	err := executeOperatorProbe(t, nil, func(cmd *cobra.Command, _ []string) error {
		runCtx = cmd.Context()
		return errProbe
	})

	require.ErrorIs(t, err, errProbe)
	require.NotNil(t, runCtx)
	require.Eventually(t, func() bool { return runCtx.Err() != nil }, time.Second, 10*time.Millisecond)
}

func TestOperatorPreRunEErrorCancelsGroup(t *testing.T) {
	var runCtx context.Context

	probe := func(cmd *cobra.Command, _ []string) error { return nil }

	viper.Reset()
	t.Cleanup(viper.Reset)

	root := &cobra.Command{Use: "root", SilenceErrors: true, SilenceUsage: true}
	logging.AddFlags(root.PersistentFlags())

	op := OperatorsCmd()
	op.AddCommand(withLifecycle(&cobra.Command{
		Use: probeCmd,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			runCtx = cmd.Context()
			return errProbe
		},
		RunE: probe,
	}))
	root.AddCommand(op)

	ctx := context.Background()
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeConfig, &rest.Config{Host: "http://127.0.0.1:1"})
	ctx = context.WithValue(ctx, fromctx.CtxKeyKubeClientSet, kubernetes.Interface(kfake.NewSimpleClientset()))
	ctx = context.WithValue(ctx, fromctx.CtxKeyAkashClientSet, akashclientset.Interface(afake.NewSimpleClientset()))
	root.SetArgs([]string{"operator", probeCmd})

	require.ErrorIs(t, root.ExecuteContext(ctx), errProbe)
	require.NotNil(t, runCtx)
	require.Eventually(t, func() bool { return runCtx.Err() != nil }, time.Second, 10*time.Millisecond)
}

func TestOperatorSubcommandsRegistered(t *testing.T) {
	op := OperatorsCmd()
	for _, name := range []string{"inventory", "ip", "hostname"} {
		c, _, err := op.Find([]string{name})
		require.NoError(t, err)
		require.Equal(t, name, c.Name())
		require.NotNil(t, c.RunE)
	}
}
