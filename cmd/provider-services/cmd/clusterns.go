package cmd

import (
	"fmt"

	mcli "github.com/akash-network/node/x/market/client/cli"
	"github.com/spf13/cobra"

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
			lid, err := mcli.LeaseIDFromFlags(cmd.Flags())
			if err != nil {
				return err
			}
			fmt.Println(cutil.LeaseIDToNamespace(lid))
			return nil
		},
	}
	mcli.AddLeaseIDFlags(cmd.Flags())
	mcli.MarkReqLeaseIDFlags(cmd)
	return cmd
}
