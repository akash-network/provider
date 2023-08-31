package cmd

import (
	"github.com/spf13/cobra"

	"github.com/akash-network/provider/cmd/provider-services/cmd/migrate/v2beta2"
)

func migrate() *cobra.Command {
	cmd := &cobra.Command{
		Use: "migrate",
	}

	cmd.AddCommand(v2beta2.MigrateCmd())

	return cmd
}
