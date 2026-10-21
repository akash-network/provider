package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
)

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rootCmd := pcmd.NewRootCmd()

	return pcmd.ExecuteWithCtx(ctx, rootCmd, "AP")
}

func main() {
	err := run()
	if err != nil {
		os.Exit(1)
	}
}
