package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	sdkclient "github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cflags "pkg.akt.dev/go/cli/flags"
	aclient "pkg.akt.dev/go/node/client/v1beta3"
	dtypes "pkg.akt.dev/go/node/deployment/v1"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	apclient "pkg.akt.dev/go/provider/client"
	ajwt "pkg.akt.dev/go/util/jwt"
	"pkg.akt.dev/node/app"
	cutils "pkg.akt.dev/node/x/cert/utils"
)

const (
	FlagService  = "service"
	flagOutput   = "output"
	flagFollow   = "follow"
	flagTail     = "tail"
	flagAuthType = "auth-type"
)

const (
	outputText = "text"
	outputYAML = "yaml"
	outputJSON = "json"

	authTypeJWT  = "jwt"
	authTypeMTLS = "mtls"
)

var (
	errNoActiveLease = errors.New("no active leases found")
)

func addCmdFlags(cmd *cobra.Command) {
	cmd.Flags().String(cflags.FlagProvider, "", "provider")
	cmd.Flags().String(cflags.FlagOwner, "", "deployment owner")
	cmd.Flags().Uint64(cflags.FlagDSeq, 0, "deployment sequence")
	cmd.Flags().String(cflags.FlagHome, app.DefaultHome, "the application home directory")
	cmd.Flags().String(cflags.FlagFrom, "", "name or address of private key with which to sign")
	cmd.Flags().String(cflags.FlagKeyringBackend, cflags.DefaultKeyringBackend, "select keyring's backend (os|file|kwallet|pass|test)")
	cmd.Flags().String(cflags.FlagSignMode, cflags.SignModeDirect, "")

	if err := cmd.MarkFlagRequired(cflags.FlagDSeq); err != nil {
		panic(err.Error())
	}

	if err := cmd.MarkFlagRequired(cflags.FlagFrom); err != nil {
		panic(err.Error())
	}

	_ = cmd.Flags().MarkHidden(cflags.FlagOwner)
}

func addAuthFlags(cmd *cobra.Command) {
	cmd.Flags().String(flagAuthType, authTypeJWT, "gateway auth type. jwt|mtls. defaults to mtls")
}

func addManifestFlags(cmd *cobra.Command) {
	addCmdFlags(cmd)

	cmd.Flags().Uint32(cflags.FlagGSeq, 1, "group sequence")
	cmd.Flags().Uint32(cflags.FlagOSeq, 1, "order sequence")
}

func addLeaseFlags(cmd *cobra.Command) {
	addManifestFlags(cmd)

	if err := cmd.MarkFlagRequired(cflags.FlagProvider); err != nil {
		panic(err.Error())
	}
}

func addServiceFlags(cmd *cobra.Command) {
	addLeaseFlags(cmd)

	cmd.Flags().String(FlagService, "", "name of service to query")
}

func dseqFromFlags(flags *pflag.FlagSet) (uint64, error) {
	return flags.GetUint64(cflags.FlagDSeq)
}

func leaseIDFromFlags(flags *pflag.FlagSet, owner string) (mtypes.LeaseID, error) {
	str, err := flags.GetString(cflags.FlagProvider)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	provider, err := sdk.AccAddressFromBech32(str)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	dseq, err := flags.GetUint64(cflags.FlagDSeq)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	gseq, err := flags.GetUint32(cflags.FlagGSeq)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	oseq, err := flags.GetUint32(cflags.FlagOSeq)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	return mtypes.LeaseID{
		Owner:    owner,
		DSeq:     dseq,
		GSeq:     gseq,
		OSeq:     oseq,
		Provider: provider.String(),
	}, nil
}

func providerFromFlags(flags *pflag.FlagSet) (sdk.Address, error) {
	provider, err := flags.GetString(cflags.FlagProvider)
	if err != nil {
		return nil, err
	}
	addr, err := sdk.AccAddressFromBech32(provider)
	if err != nil {
		return nil, err
	}

	return addr, nil
}

func leasesForDeployment(ctx context.Context, cl aclient.QueryClient, flags *pflag.FlagSet, did dtypes.DeploymentID) ([]mtypes.LeaseID, error) {
	filter := mtypes.LeaseFilters{
		Owner: did.Owner,
		DSeq:  did.DSeq,
		State: mtypes.Lease_State_name[int32(mtypes.LeaseActive)],
	}

	if flags.Changed(cflags.FlagProvider) {
		prov, err := providerFromFlags(flags)
		if err != nil {
			return nil, err
		}

		filter.Provider = prov.String()
	}

	if val, err := flags.GetUint32(cflags.FlagGSeq); flags.Changed(cflags.FlagGSeq) && err == nil {
		filter.GSeq = val
	}

	if val, err := flags.GetUint32(cflags.FlagOSeq); flags.Changed(cflags.FlagOSeq) && err == nil {
		filter.OSeq = val
	}

	resp, err := cl.Market().Leases(ctx, &mvbeta.QueryLeasesRequest{
		Filters: filter,
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Leases) == 0 {
		return nil, fmt.Errorf("%w  for dseq=%v", errNoActiveLease, did.DSeq)
	}

	leases := make([]mtypes.LeaseID, 0, len(resp.Leases))

	for _, lease := range resp.Leases {
		leases = append(leases, lease.Lease.ID)
	}

	return leases, nil
}

func markRPCServerError(err error) error {
	var jsonError *json.SyntaxError
	var urlError *url.Error

	switch {
	case errors.As(err, &jsonError):
		fallthrough
	case errors.As(err, &urlError):
		return fmt.Errorf("error communicating with RPC server: %w", err)
	}

	return err
}

func loadAuthOpts(ctx context.Context, cctx sdkclient.Context, flags *pflag.FlagSet) ([]apclient.ClientOption, error) {
	authType, err := flags.GetString(flagAuthType)
	if err != nil {
		return nil, err
	}

	var opts []apclient.ClientOption
	switch authType {
	case authTypeJWT:
		opt := apclient.WithAuthJWTSigner(ajwt.NewSigner(cctx.Keyring, cctx.FromAddress))
		opts = append(opts, opt)
	case authTypeMTLS:
		cert, err := cutils.LoadAndQueryCertificateForAccount(ctx, cctx, nil)
		if err != nil {
			return nil, err
		}
		opt := apclient.WithAuthCerts([]tls.Certificate{cert})
		opts = append(opts, opt)
	default:
		return nil, fmt.Errorf("invalid gateway auth type %s. supported are jwt|mtls", authType)
	}

	return opts, nil
}
