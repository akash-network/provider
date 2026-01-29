package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	cflags "pkg.akt.dev/go/cli/flags"

	cutil "github.com/akash-network/provider/cluster/util"
)

func clusterNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "show-cluster-ns",
		Aliases:      []string{"cluster-ns"},
		Short:        "print cluster namespace for given lease ID",
		Args:         cobra.ExactArgs(0),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lid, err := cflags.LeaseIDFromFlags(cmd.Flags())
			if err != nil {
				return err
			}
			fmt.Println(cutil.LeaseIDToNamespace(lid))
			return nil
		},
	}

	cflags.AddLeaseIDFlags(cmd.Flags())
	cflags.MarkReqLeaseIDFlags(cmd)

	return cmd
}
