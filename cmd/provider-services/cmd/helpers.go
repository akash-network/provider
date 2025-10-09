package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	aclient "github.com/akash-network/akash-api/go/node/client/v1beta2"
	ptypes "github.com/akash-network/akash-api/go/node/provider/v1beta3"
	apclient "github.com/akash-network/akash-api/go/provider/client"
	ajwt "github.com/akash-network/akash-api/go/util/jwt"
	cutils "github.com/akash-network/node/x/cert/utils"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	pclient "github.com/akash-network/provider/client"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	mtypes "github.com/akash-network/akash-api/go/node/market/v1beta4"
	"github.com/akash-network/node/app"
)

const (
	FlagService     = "service"
	FlagProvider    = "provider"
	FlagProviderURL = "provider-url"
	FlagDSeq        = "dseq"
	FlagGSeq        = "gseq"
	FlagOSeq        = "oseq"
	flagOutput      = "output"
	flagFollow      = "follow"
	flagTail        = "tail"
	flagAuthType    = "auth-type"
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

func addProviderURLFlag(cmd *cobra.Command) error {
	cmd.Flags().String(FlagProviderURL, "", "Provider URL to connect to directly")
	if err := viper.BindPFlag(FlagProviderURL, cmd.Flags().Lookup(FlagProviderURL)); err != nil {
		return err
	}
	return nil
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

func providerFromFlags(flags *pflag.FlagSet) (sdk.AccAddress, error) {
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

//// providerClientFromFlags creates an akash provider client with appropriate options
//// based on whether a provider URL is specified via flags or not.
//// If provider-url flag is set, it uses that URL directly.
//// Otherwise, it queries the blockchain for the provider's URL.
//func providerClientFromFlags(
//	ctx context.Context,
//	cl aclient.QueryClient,
//	prov sdk.Address,
//	opts []apclient.ClientOption,
//	flags *pflag.FlagSet,
//) (apclient.Client, error) {
//	providerURL, err := flags.GetString(FlagProviderURL)
//	if err != nil {
//		return nil, err
//	}
//
//	// If no provider URL is provided via flag, query the blockchain for it
//	if providerURL == "" {
//		resp, err := cl.Provider(ctx, &ptypes.QueryProviderRequest{
//			Owner: prov.String(),
//		})
//		if err != nil {
//			return nil, err
//		}
//		providerURL = resp.Provider.HostURI
//	}
//
//	if cl != nil {
//		opts = append(opts, apclient.WithCertQuerier(client.NewCertificateQuerier(cl)))
//	}
//
//	// Always add the provider URL - the client requires it
//	opts = append(opts, apclient.WithProviderURL(providerURL))
//
//	return apclient.NewClient(ctx, prov, opts...)
//}
//
//// ProviderURLHandlerResult contains the results from provider URL handling
//type ProviderURLHandlerResult struct {
//	QueryClient aclient.QueryClient
//	LeaseIDs    []mtypes.LeaseID
//	LeaseID     mtypes.LeaseID
//	Provider    sdk.Address
//}
//
//// OnChainCallback defines the callback function type for on-chain specific behavior
//type OnChainCallback func(ctx context.Context, cctx sdkclient.Context, cl aclient.QueryClient, flags *pflag.FlagSet) (ProviderURLHandlerResult, error)
//
//// handleProviderURLOrOnChain handles the common pattern of checking provider URL flag
//// and either using direct provider connection or falling back to on-chain discovery.
//// It takes a callback function to handle the on-chain specific logic.
//func handleProviderURLOrOnChain(
//	ctx context.Context,
//	cctx sdkclient.Context,
//	flags *pflag.FlagSet,
//	onChainCallback OnChainCallback,
//) (ProviderURLHandlerResult, error) {
//	providerURL := viper.GetString(FlagProviderURL)
//
//	if providerURL != "" {
//		leaseID, err := leaseIDFromFlags(flags, cctx.GetFromAddress().String())
//		if err != nil {
//			return ProviderURLHandlerResult{}, err
//		}
//
//		prov, err := providerFromFlags(flags)
//		if err != nil {
//			return ProviderURLHandlerResult{}, err
//		}
//
//		return ProviderURLHandlerResult{
//			LeaseID:  leaseID,
//			LeaseIDs: []mtypes.LeaseID{leaseID},
//			Provider: prov,
//		}, nil
//	}
//
//	cl, err := pclient.DiscoverQueryClient(ctx, cctx)
//	if err != nil {
//		return ProviderURLHandlerResult{}, err
//	}
//
//	return onChainCallback(ctx, cctx, cl, flags)
//}

func setupProviderClient(ctx context.Context, cctx sdkclient.Context, flags *pflag.FlagSet) (apclient.Client, error) {
	paddr, err := providerFromFlags(flags)
	if err != nil {
		return nil, err
	}

	purl := viper.GetString(FlagProviderURL)
	if purl == "" {
		cl, err := pclient.DiscoverQueryClient(ctx, cctx)
		if err != nil {
			return nil, err
		}

		resp, err := cl.Provider(ctx, &ptypes.QueryProviderRequest{Owner: paddr.String()})
		if err != nil {
			return nil, err
		}

		purl = resp.Provider.HostURI
	}

	opts, err := loadAuthOpts(ctx, cctx, flags)
	if err != nil {
		return nil, err
	}

	// Always add the provider URL - the client requires it
	opts = append(opts, apclient.WithProviderURL(purl))

	cl, err := apclient.NewClient(ctx, paddr, opts...)
	if err != nil {
		return nil, err
	}

	return cl, nil
}
