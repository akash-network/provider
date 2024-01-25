package main

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"github.com/cosmos/cosmos-sdk/server"

	acmd "github.com/akash-network/node/cmd/akash/cmd"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
)

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rootCmd := pcmd.NewRootCmd()

	return acmd.ExecuteWithCtx(ctx, rootCmd, "AP")
}

func main() {
	err := run()
	if err != nil {
		if errors.As(err, &server.ErrorCode{}) {
			os.Exit(err.(server.ErrorCode).Code)
		}

		os.Exit(1)
	}
}
