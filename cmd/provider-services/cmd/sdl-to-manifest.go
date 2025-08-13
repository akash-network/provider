package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	sdltypes "pkg.akt.dev/go/sdl"
)

// SDL2ManifestCmd dump manifest into stdout
func SDL2ManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sdl-to-manifest <sdl-path>",
		Args:         cobra.ExactArgs(1),
		Short:        "Dump manifest derived from the SDL",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			format := cmd.Flag(flagOutput).Value.String()
			switch format {
			case outputJSON:
			case outputYAML:
			default:
				return fmt.Errorf("invalid output format \"%s\", expected json|yaml", format) // nolint: err113
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			sdl, err := sdltypes.ReadFile(args[0])
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
