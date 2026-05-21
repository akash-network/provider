package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/akash-network/provider/verification/inventory/hostprobe"
)

const (
	flagOutput  = "output"
	flagPretty  = "pretty"
	flagRoot    = "root"
	flagTimeout = "source-timeout"
)

type rootOptions struct {
	output  string
	pretty  bool
	root    string
	timeout time.Duration
}

func newRootCmd() *cobra.Command {
	opts := rootOptions{
		output:  "-",
		root:    "/",
		timeout: 2 * time.Second,
	}

	cmd := &cobra.Command{
		Use:          "akash-inventory",
		Short:        "collect AEP-86 host inventory evidence",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRoot(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.output, flagOutput, opts.output, "output path or - for stdout")
	cmd.Flags().BoolVar(&opts.pretty, flagPretty, opts.pretty, "pretty-print JSON output")
	cmd.Flags().StringVar(&opts.root, flagRoot, opts.root, "filesystem root to probe")
	cmd.Flags().DurationVar(&opts.timeout, flagTimeout, opts.timeout, "per-source collection timeout")

	return cmd
}

func runRoot(cmd *cobra.Command, opts rootOptions) error {
	collector := hostprobe.NewCollector(hostprobe.Config{
		Root:          opts.root,
		SourceTimeout: opts.timeout,
	})

	snapshot, err := collector.Snapshot(cmd.Context())
	if err != nil {
		return err
	}

	var payload []byte
	if opts.pretty {
		payload, err = json.MarshalIndent(snapshot, "", "  ")
	} else {
		payload, err = json.Marshal(snapshot)
	}
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	if opts.output == "-" {
		_, err = cmd.OutOrStdout().Write(payload)
		return err
	}

	return os.WriteFile(opts.output, payload, 0o644)
}
