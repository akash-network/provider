// Package version
// is a convenience utility that provides provider
// consumers with a ready-to-use version command that
// produces apps versioning information based on flags
// passed at compile time.
//
// Configure the version command
//
// The version command can be just added to your cobra root command.
// At build time, the variables Name, Version, Commit, and BuildTags
// can be passed as build flags as shown in the following example:
//
//  go build -X github.com/ovrclk/provider-services/version.Name=provider-services \
//   -X github.com/ovrclk/provider-services/version.AppName=provider-services \
//   -X github.com/ovrclk/provider-services/version.Version=1.0 \
//   -X github.com/ovrclk/provider-services/version.Commit=f0f7b7dab7e36c20b757cebce0e8f4fc5b95de60 \
//   -X "github.com/ovrclk/provider-services/version.BuildTags=linux darwin amd64"
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tendermint/tendermint/libs/cli"
	"gopkg.in/yaml.v3"
)

var (
	// application's name
	Name = ""
	// application binary name
	AppName = "<appd>"
	// application's version string
	Version = ""
	// commit
	Commit = ""
	// build tags
	BuildTags = ""
)

const flagLong = "long"

// NewVersionCommand returns a CLI command to interactively print the application binary version information.
func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the application binary version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			verInfo := NewInfo()

			if long, _ := cmd.Flags().GetBool(flagLong); !long {
				cmd.Println(verInfo.Version)
				return nil
			}

			var (
				bz  []byte
				err error
			)

			output, _ := cmd.Flags().GetString(cli.OutputFlag)
			switch strings.ToLower(output) {
			case "json":
				bz, err = json.Marshal(verInfo)

			default:
				bz, err = yaml.Marshal(&verInfo)
			}

			if err != nil {
				return err
			}

			cmd.Println(string(bz))
			return nil
		},
	}

	cmd.Flags().Bool(flagLong, false, "Print long version information")
	cmd.Flags().StringP(cli.OutputFlag, "o", "text", "Output format (text|json)")

	return cmd
}

// Info defines the application version information.
type Info struct {
	Name      string     `json:"name" yaml:"name"`
	AppName   string     `json:"server_name" yaml:"server_name"`
	Version   string     `json:"version" yaml:"version"`
	GitCommit string     `json:"commit" yaml:"commit"`
	BuildTags string     `json:"build_tags" yaml:"build_tags"`
	GoVersion string     `json:"go" yaml:"go"`
	BuildDeps []buildDep `json:"build_deps" yaml:"build_deps"`
}

func NewInfo() Info {
	return Info{
		Name:      Name,
		AppName:   AppName,
		Version:   Version,
		GitCommit: Commit,
		BuildTags: BuildTags,
		GoVersion: fmt.Sprintf("go version %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		BuildDeps: depsFromBuildInfo(),
	}
}

func (vi Info) String() string {
	return fmt.Sprintf(`%s: %s
git commit: %s
build tags: %s
%s`,
		vi.Name, vi.Version, vi.GitCommit, vi.BuildTags, vi.GoVersion,
	)
}

func depsFromBuildInfo() (deps []buildDep) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	for _, dep := range buildInfo.Deps {
		deps = append(deps, buildDep{dep})
	}

	return
}

type buildDep struct {
	*debug.Module
}

func (d buildDep) String() string {
	if d.Replace != nil {
		return fmt.Sprintf("%s@%s => %s@%s", d.Path, d.Version, d.Replace.Path, d.Replace.Version)
	}

	return fmt.Sprintf("%s@%s", d.Path, d.Version)
}

func (d buildDep) MarshalJSON() ([]byte, error)      { return json.Marshal(d.String()) }
func (d buildDep) MarshalYAML() (interface{}, error) { return d.String(), nil }
