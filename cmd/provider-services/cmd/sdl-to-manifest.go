package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/akash-network/node/sdl"
)

// SDL2ManifestCmd dump manifest into stdout
func SDL2ManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sdl-to-manifest <sdl-path>",
		Args:         cobra.ExactArgs(1),
		Short:        "Dump manifest derived from the SDL",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			format := cmd.Flag(flagOutput).Value.String()
			switch format {
			case outputJSON:
			case outputYAML:
			default:
				return errors.New(fmt.Sprintf("invalid output format \"%s\", expected json|yaml", format)) // nolint: goerr113, revive
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			sdl, err := sdl.ReadFile(args[0])
			if err != nil {
				return err
			}

			mani, err := sdl.Manifest()
			if err != nil {
				return err
			}

			var data []byte

			switch cmd.Flag(flagOutput).Value.String() {
			case outputJSON:
				data, err = json.MarshalIndent(mani, "", "  ")
			case outputYAML:
				data, err = yaml.Marshal(mani)
			}

			if err != nil {
				return err
			}

			fmt.Println(string(data))

			return nil
		},
	}

	cmd.Flags().StringP(flagOutput, "o", outputJSON, "output format json|yaml. default json")

	return cmd
}
