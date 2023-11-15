package cmd

import (
	"github.com/spf13/cobra"
)

func migrate() *cobra.Command {
	cmd := &cobra.Command{
		Use: "migrate",
	}

	return cmd
}
