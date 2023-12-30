package operator

import (
	"github.com/spf13/cobra"

	"github.com/akash-network/provider/operator/inventory"
)

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "operator",
		Short:        "kubernetes operators control",
		SilenceUsage: true,
	}

	cmd.AddCommand(inventory.Cmd())
	cmd.AddCommand(cmdPsutil())

	return cmd
}
