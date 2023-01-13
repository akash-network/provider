package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/server"

	acmd "github.com/akash-network/node/cmd/akash/cmd"

	pcmd "github.com/akash-network/provider/cmd/provider-services/cmd"
)

// In main we call the rootCmd
func main() {
	rootCmd := pcmd.NewRootCmd()

	if err := acmd.Execute(rootCmd, "AP"); err != nil {
		switch e := err.(type) {
		case server.ErrorCode:
			os.Exit(e.Code)
		default:
			os.Exit(1)
		}
	}
}
