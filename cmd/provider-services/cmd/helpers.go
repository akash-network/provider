package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	apclient "github.com/akash-network/akash-api/go/provider/client"
	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	cutils "github.com/akash-network/node/x/cert/utils"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/app"
)

const (
	FlagService  = "service"
	FlagProvider = "provider"
	FlagDSeq     = "dseq"
	FlagGSeq     = "gseq"
	FlagOSeq     = "oseq"
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
	cmd.Flags().String(FlagProvider, "", "provider")
	cmd.Flags().Uint64(FlagDSeq, 0, "deployment sequence")
	cmd.Flags().String(flags.FlagHome, app.DefaultHome, "the application home directory")
	cmd.Flags().String(flags.FlagFrom, "", "name or address of private key with which to sign")
	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "select keyring's backend (os|file|kwallet|pass|test)")

	if err := cmd.MarkFlagRequired(FlagDSeq); err != nil {
		panic(err.Error())
	}

	if err := cmd.MarkFlagRequired(flags.FlagFrom); err != nil {
		panic(err.Error())
	}
}

func addAuthFlags(cmd *cobra.Command) {
	cmd.Flags().String(flagAuthType, authTypeJWT, "gateway auth type. jwt|mtls. defaults to mtls")
}

func addManifestFlags(cmd *cobra.Command) {
	addCmdFlags(cmd)

	cmd.Flags().Uint32(FlagGSeq, 1, "group sequence")
	cmd.Flags().Uint32(FlagOSeq, 1, "order sequence")
}

func addLeaseFlags(cmd *cobra.Command) {
	addManifestFlags(cmd)

	if err := cmd.MarkFlagRequired(FlagProvider); err != nil {
		panic(err.Error())
	}
}

func addServiceFlags(cmd *cobra.Command) {
	addLeaseFlags(cmd)

	cmd.Flags().String(FlagService, "", "name of service to query")
}

func dseqFromFlags(flags *pflag.FlagSet) (uint64, error) {
	return flags.GetUint64(FlagDSeq)
}

func leaseIDFromFlags(cflags *pflag.FlagSet, owner string) (mtypes.LeaseID, error) {
	str, err := cflags.GetString(FlagProvider)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	provider, err := sdk.AccAddressFromBech32(str)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	dseq, err := cflags.GetUint64(FlagDSeq)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	gseq, err := cflags.GetUint32(FlagGSeq)
	if err != nil {
		return mtypes.LeaseID{}, err
	}

	oseq, err := cflags.GetUint32(FlagOSeq)
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
	provider, err := flags.GetString(FlagProvider)
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

	if flags.Changed(FlagProvider) {
		prov, err := providerFromFlags(flags)
		if err != nil {
			return nil, err
		}

		filter.Provider = prov.String()
	}

	if val, err := flags.GetUint32(FlagGSeq); flags.Changed(FlagGSeq) && err == nil {
		filter.GSeq = val
	}

	if val, err := flags.GetUint32(FlagOSeq); flags.Changed(FlagOSeq) && err == nil {
		filter.OSeq = val
	}

	resp, err := cl.Leases(ctx, &mtypes.QueryLeasesRequest{
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
		leases = append(leases, lease.Lease.LeaseID)
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
