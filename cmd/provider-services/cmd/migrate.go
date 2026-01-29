package cmd

import (
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
)

func migrate() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "migrate",
		PersistentPreRunE: cli.TxPersistentPreRunE,
	}

	return cmd
}
