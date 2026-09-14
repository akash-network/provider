//go:build e2e

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	providerBinaryOnce sync.Once
	providerBinaryPath string
	providerBinaryErr  error
)

func buildProviderBinary(t *testing.T) string {
	t.Helper()

	providerBinaryOnce.Do(func() {
		repoRoot, err := filepath.Abs("..")
		if err != nil {
			providerBinaryErr = err
			return
		}

		// Cached across suites via sync.Once, so it must outlive any single
		// test's TempDir, which is why this is not t.TempDir().
		dir, err := os.MkdirTemp("", "akash-provider-bin")
		if err != nil {
			providerBinaryErr = err
			return
		}

		bin := filepath.Join(dir, "provider-services")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/provider-services")
		cmd.Dir = repoRoot
		cmd.Env = os.Environ()
		if out, cerr := cmd.CombinedOutput(); cerr != nil {
			providerBinaryErr = fmt.Errorf("building provider binary: %w\n%s", cerr, out)
			return
		}
		providerBinaryPath = bin
	})

	require.NoError(t, providerBinaryErr)
	return providerBinaryPath
}

func buildProviderBinaryAtRef(t *testing.T, ref string) string {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", "akash-provider-worktree")
	require.NoError(t, err)

	// The binary is built into a directory outside the worktree so it survives the
	// worktree removal below; only the checkout is transient.
	bindir, err := os.MkdirTemp("", "akash-provider-bin-ref")
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = os.RemoveAll(bindir)
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", dir).Run()
		_ = os.RemoveAll(dir)
	})

	add := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", dir, ref)
	if out, cerr := add.CombinedOutput(); cerr != nil {
		t.Fatalf("adding worktree for ref %q: %v\n%s", ref, cerr, out)
	}

	bin := filepath.Join(bindir, "provider-services")
	build := exec.Command("go", "build", "-mod=readonly", "-o", bin, "./cmd/provider-services")
	build.Dir = dir
	build.Env = os.Environ()
	if out, cerr := build.CombinedOutput(); cerr != nil {
		// -mod=mod may mutate the worktree's go.mod, which is discarded with the
		// worktree; it must never run against the main tree.
		retry := exec.Command("go", "build", "-mod=mod", "-o", bin, "./cmd/provider-services")
		retry.Dir = dir
		retry.Env = os.Environ()
		if rout, rerr := retry.CombinedOutput(); rerr != nil {
			t.Fatalf("building provider binary at ref %q: %v\n%s\n%s", ref, cerr, out, rout)
		}
	}

	remove := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", dir)
	if out, cerr := remove.CombinedOutput(); cerr != nil {
		t.Fatalf("removing worktree %q: %v\n%s", dir, cerr, out)
	}

	return bin
}

// prefixWriter forwards subprocess output straight to os.Stderr (raw and unmanaged,
// interleaved into the test process output, not captured per-test). It deliberately
// does NOT hold a *testing.T: the last relaunched provider outlives the test method
// that spawned it (it is killed only in TearDownSuite), so logging through a by-then-
// completed subtest T would panic the whole binary.
type prefixWriter struct{ prefix string }

func (w prefixWriter) Write(p []byte) (int, error) {
	// Prefix every line, not just the first of a chunk, so no subprocess line lands
	// at column 0 where a log scanner could mistake e.g. a "panic:" message for the
	// test binary's own output.
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

	// The in-process launcher binds a fully dialed client.Context (chain client,
	// keyring, home) into the command with no flags; a subprocess cannot receive
	// that object, so the equivalent connection settings arrive as explicit flags.
	cmd := exec.CommandContext(ctx, bin, append([]string{"run"}, args...)...)
	cmd.Env = os.Environ()
	w := prefixWriter{prefix: "[provider]"}
	cmd.Stdout = w
	cmd.Stderr = w

	// A real restart/upgrade sends SIGTERM and lets the provider shut down gracefully,
	// escalating to a kill only if it hangs. exec.CommandContext defaults to SIGKILL,
	// which would test crash recovery, not the graceful path.
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
