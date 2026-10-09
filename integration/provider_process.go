//go:build e2e

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func providerBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("AKASH_PROVIDER_BIN")
	require.NotEmptyf(t, bin, "AKASH_PROVIDER_BIN is unset; run the gates via `make test-e2e-provider-gates`, which builds the provider binaries")
	return bin
}

// providerBaseBinary returns the upgrade gate's base-version binary, falling back to the
// current binary (a same-version check) when no base ref was built.
func providerBaseBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("AKASH_PROVIDER_BASE_BIN"); bin != "" {
		return bin
	}
	return providerBinary(t)
}

// prefixWriter writes to os.Stderr and deliberately holds no *testing.T: the last
// relaunched provider outlives its test method (killed only in TearDownSuite), so a
// captured T would panic the binary when it logs after the test completes.
type prefixWriter struct{ prefix string }

func (w prefixWriter) Write(p []byte) (int, error) {
	// Prefix every line so no subprocess output lands at column 0, where the summary
	// scanner could mistake e.g. a "panic:" line for the test binary's own.
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		fmt.Fprintf(os.Stderr, "%s %s\n", w.prefix, line)
	}
	return len(p), nil
}

type providerProcess struct {
	cmd  *exec.Cmd
	done chan error
}

func startProviderProcess(ctx context.Context, t *testing.T, bin string, args []string) *providerProcess {
	t.Helper()

	cmd := exec.CommandContext(ctx, bin, append([]string{"run"}, args...)...)
	cmd.Env = os.Environ()
	w := prefixWriter{prefix: "[provider]"}
	cmd.Stdout = w
	cmd.Stderr = w

	// SIGTERM then kill-after-delay: exercise graceful shutdown, not the SIGKILL that
	// exec.CommandContext defaults to (which would test crash recovery instead).
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 30 * time.Second

	require.NoError(t, cmd.Start())

	p := &providerProcess{
		cmd:  cmd,
		done: make(chan error, 1),
	}
	go func() {
		p.done <- cmd.Wait()
	}()

	return p
}

func (p *providerProcess) wait() error {
	return <-p.done
}
