package logging

import (
	"context"
	"os"
	"strings"

	"cosmossdk.io/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"pkg.akt.dev/go/util/ctxlog"
)

type configCtxKey struct{}

var optionNames = []string{FlagLogLevel, FlagLogFormat, FlagLogColor, FlagLogTimestamp, FlagTrace}

// AddFlags registers log_level, log_format, log_color and log_timestamp with the
// provider defaults. The trace flag is registered by cometbft's PrepareBaseCmd.
func AddFlags(fs *pflag.FlagSet) {
	fs.String(FlagLogLevel, DefaultLevel, "The logging level (trace|debug|info|warn|error|fatal|panic) or module filter \"module:level,...,*:level\"")
	fs.String(FlagLogFormat, DefaultFormat, "The logging format (plain|json)")
	fs.Bool(FlagLogColor, DefaultColor, "Colored logging output. Ignored when log_format=json")
	fs.String(FlagLogTimestamp, DefaultTimestamp, "Timestamp format of the logs (off|rfc3339|rfc3339nano|kitchen)")
}

// NormalizeTimestampForUpstream rewrites an effective log_timestamp of "off" to ""
// on the command's flag, so pkg.akt.dev/go/cli's root pre-run (which accepts only
// ""|rfc3339|rfc3339nano|kitchen) does not reject it. The effective value is taken,
// in order, from the changed flag, <prefix>_LOG_TIMESTAMP for each env prefix, and
// the global viper (config file).
func NormalizeTimestampForUpstream(cmd *cobra.Command, envPrefixes ...string) error {
	flag := cmd.Flags().Lookup(FlagLogTimestamp)
	if flag == nil {
		return nil
	}

	if !isOff(effectiveTimestamp(flag, envPrefixes)) {
		return nil
	}

	return cmd.Flags().Set(FlagLogTimestamp, "")
}

func effectiveTimestamp(flag *pflag.Flag, envPrefixes []string) string {
	if flag.Changed {
		return flag.Value.String()
	}

	for _, prefix := range envPrefixes {
		if val, ok := os.LookupEnv(strings.ToUpper(prefix) + "_LOG_TIMESTAMP"); ok && val != "" {
			return val
		}
	}

	return viper.GetString(FlagLogTimestamp)
}

func isOff(val string) bool {
	return strings.EqualFold(strings.TrimSpace(val), TimestampOff)
}

// Setup builds the logger from the logging options and stores it, together with
// its Config, in the command context. When the context already holds a logger,
// Setup returns it unchanged.
func Setup(cmd *cobra.Command) (log.Logger, error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if l, ok := ctx.Value(ctxlog.CtxKeyLog).(log.Logger); ok {
		return l, nil
	}

	for _, name := range optionNames {
		if flag := cmd.Flags().Lookup(name); flag != nil {
			if err := viper.BindPFlag(name, flag); err != nil {
				return nil, err
			}
		}
	}

	cfg, err := ConfigFromViper(viper.GetViper())
	if err != nil {
		return nil, err
	}

	l := New(os.Stdout, cfg)

	ctx = ctxlog.WithLogger(ctx, l)
	ctx = context.WithValue(ctx, configCtxKey{}, cfg)
	cmd.SetContext(ctx)

	return l, nil
}

// ConfigFromCtx returns the configuration stored by Setup.
func ConfigFromCtx(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(configCtxKey{}).(Config)
	return cfg, ok
}
