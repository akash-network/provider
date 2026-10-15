package operator

import (
	"context"

	"github.com/spf13/cobra"
)

type runCancelKey struct{}

// withLifecycle wraps an operator subcommand so the run context created by the
// operator pre-run is cancelled when the subcommand returns, including when its
// PreRunE or RunE fails before signalling startup. This releases the goroutine
// waiting for startup and cancels the pubsub context.
func withLifecycle(cmd *cobra.Command) *cobra.Command {
	if preRunE := cmd.PreRunE; preRunE != nil {
		cmd.PreRunE = func(c *cobra.Command, args []string) error {
			err := preRunE(c, args)
			if err != nil {
				cancelRun(c.Context())
			}

			return err
		}
	}

	if runE := cmd.RunE; runE != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			defer cancelRun(c.Context())

			return runE(c, args)
		}
	}

	return cmd
}

func cancelRun(ctx context.Context) {
	if cancel, ok := ctx.Value(runCancelKey{}).(context.CancelFunc); ok {
		cancel()
	}
}
