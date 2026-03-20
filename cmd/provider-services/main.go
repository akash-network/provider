package main

import (
	"context"
	"os"
	"os/signal"

	"pkg.akt.dev/go/cli"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
)

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rootCmd := pcmd.NewRootCmd()

	return cli.ExecuteWithCtx(ctx, rootCmd, "AP")
}

func main() {
	err := run()
	if err != nil {
		os.Exit(1)
	}
}
