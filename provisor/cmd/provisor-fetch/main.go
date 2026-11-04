package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/akash-network/provider/provisor/release"
	"github.com/akash-network/provider/provisor/trust"
	"github.com/akash-network/provider/provisor/verify"
)

const defaultAllowedRegistry = "ghcr.io/akash-network"

func main() {
	keySetURL := flag.String("keyset-url", "", "URL serving the signed key-set envelope (required)")
	channelURL := flag.String("channel-url", "", "URL serving the signed channel pointer envelope (required)")
	installedRelease := flag.String("installed-release", "", "the release currently installed (required)")
	lastKeySetVersion := flag.String("last-keyset-version", "0", "the highest key-set version already observed")
	allowedRegistries := newStringSliceFlag(defaultAllowedRegistry)
	flag.Var(allowedRegistries, "allowed-registry", "registry path a chart or image reference may come from (repeatable; default "+defaultAllowedRegistry+")")
	flag.Parse()

	if *keySetURL == "" || *channelURL == "" || *installedRelease == "" {
		fmt.Fprintln(os.Stderr, "provisor-fetch: --keyset-url, --channel-url and --installed-release are required")
		os.Exit(2)
	}

	roots, err := trust.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "provisor-fetch: loading compiled-in trust roots: %v\n", err)
		os.Exit(1)
	}

	verifier := &verify.Verifier{Client: http.DefaultClient, Now: time.Now, Roots: roots}
	input := verify.Input{
		KeySetURL:         *keySetURL,
		ChannelURL:        *channelURL,
		LastKeySetVersion: release.Version(*lastKeySetVersion),
		Installed: release.InstalledState{
			Release:           release.Version(*installedRelease),
			AllowedRegistries: allowedRegistries.values,
		},
	}

	result := verifier.Verify(context.Background(), input)
	if result.Reason != "" {
		fmt.Println(result.Reason)
		os.Exit(1)
	}

	fmt.Printf("accepted release %s\n", result.Manifest.Release)
}
