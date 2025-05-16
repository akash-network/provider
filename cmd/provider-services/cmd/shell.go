package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	apclient "github.com/akash-network/akash-api/go/provider/client"
	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	"github.com/go-andiamo/splitter"
	dockerterm "github.com/moby/term"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/util/term"

	sdkclient "github.com/cosmos/cosmos-sdk/client"

	dcli "github.com/akash-network/node/x/deployment/client/cli"
	mcli "github.com/akash-network/node/x/market/client/cli"

	aclient "github.com/akash-network/provider/client"
)

const (
	FlagStdin        = "stdin"
	FlagTty          = "tty"
	FlagReplicaIndex = "replica-index"
)

var (
	errTerminalNotATty = errors.New("input is not a terminal, cannot setup TTY")
)

func LeaseShellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Args:         cobra.MinimumNArgs(2),
		Use:          "lease-shell",
		Short:        "do lease shell",
		SilenceUsage: true,
		RunE:         doLeaseShell,
	}

	addLeaseFlags(cmd)
	cmd.Flags().Bool(FlagStdin, false, "connect stdin")
	if err := viper.BindPFlag(FlagStdin, cmd.Flags().Lookup(FlagStdin)); err != nil {
		return nil
	}

	cmd.Flags().Bool(FlagTty, false, "connect an interactive terminal")
	if err := viper.BindPFlag(FlagTty, cmd.Flags().Lookup(FlagTty)); err != nil {
		return nil
	}

	cmd.Flags().Uint(FlagReplicaIndex, 0, "replica index to connect to")
	if err := viper.BindPFlag(FlagReplicaIndex, cmd.Flags().Lookup(FlagReplicaIndex)); err != nil {
		return nil
	}

	return cmd
}

func doLeaseShell(cmd *cobra.Command, args []string) error {
	var stdin io.Reader
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	connectStdin := viper.GetBool(FlagStdin)
	setupTty := viper.GetBool(FlagTty)
	podIndex := viper.GetUint(FlagReplicaIndex)
	if connectStdin || setupTty {
		stdin = cmd.InOrStdin()
	}

	var tty term.TTY
	var tsq remotecommand.TerminalSizeQueue
	if setupTty {
		tty = term.TTY{
			Parent: nil,
			Out:    os.Stdout,
			In:     stdin,
		}

		if !tty.IsTerminalIn() {
			return errTerminalNotATty
		}

		dockerStdin, dockerStdout, _ := dockerterm.StdStreams()

		tty.In = dockerStdin
		tty.Out = dockerStdout

		stdin = dockerStdin
		stdout = dockerStdout
		tsq = tty.MonitorSize(tty.GetSize())
		tty.Raw = true
	}

	cctx, err := sdkclient.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	cl, err := aclient.DiscoverQueryClient(ctx, cctx)
	if err != nil {
		return err
	}

	prov, err := providerFromFlags(cmd.Flags())
	if err != nil {
		return err
	}

	bidID, err := mcli.BidIDFromFlags(cmd.Flags(), dcli.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}
	lID := bidID.LeaseID()

	gclient, err := apclient.NewClient(ctx, cl, prov, apclient.WithAuthJWTSigner(ajwt.NewSigner(cctx.Keyring, cctx.FromAddress)))
	if err != nil {
		return err
	}

	service := args[0]
	remoteCmd := args[1:]

	if len(remoteCmd) == 1 {
		spaceSplitter, err := splitter.NewSplitter(' ', splitter.DoubleQuotes)
		if err != nil {
			return err
		}

		remoteCmd, err = spaceSplitter.Split(remoteCmd[0])
		if err != nil {
			return err
		}
	}

	var terminalResizes chan remotecommand.TerminalSize
	wg := &sync.WaitGroup{}
	ctx, cancel := context.WithCancel(ctx)

	if tsq != nil {
		terminalResizes = make(chan remotecommand.TerminalSize, 1)
		go func() {
			for {
				// this blocks waiting for a resize event, the docs suggest
				// that this isn't the case but there is not a code path that ever does that
				// so this goroutine is just left running until the process exits
				size := tsq.Next()
				if size == nil {
					return
				}
				terminalResizes <- *size

			}
		}()
	}

	signals := make(chan os.Signal, 1)
	signalsToCatch := []os.Signal{syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP}
	if !setupTty { // if the terminal is not interactive, handle SIGINT
		signalsToCatch = append(signalsToCatch, syscall.SIGINT)
	}
	signal.Notify(signals, signalsToCatch...)
	wasHalted := make(chan os.Signal, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case sig := <-signals:
			cancel()
			wasHalted <- sig
		case <-ctx.Done():
		}
	}()
	leaseShellFn := func() error {
		return gclient.LeaseShell(ctx, lID, service, podIndex, remoteCmd, stdin, stdout, stderr, setupTty, terminalResizes)
	}

	if setupTty { // Interactive terminals run with a wrapper that restores the prior state
		err = tty.Safe(leaseShellFn)
	} else {
		err = leaseShellFn()
	}

	// Check if a signal halted things
	select {
	case haltSignal := <-wasHalted:
		_ = cctx.PrintString(fmt.Sprintf("\nhalted by signal: %v\n", haltSignal))
		err = nil // Don't show this error, as it is always something complaining about use of a closed connection
	default:
		cancel()
	}
	wg.Wait()

	if err != nil {
		return showErrorToUser(err)
	}

	return nil
}
