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

	pclient "github.com/akash-network/provider/client"
	cflags "pkg.akt.dev/go/cli/flags"
	discovery "pkg.akt.dev/go/node/client/discovery"
	aclient "pkg.akt.dev/go/node/client/v1beta3"
	mtypes "pkg.akt.dev/go/node/market/v1"
	mvbeta "pkg.akt.dev/go/node/market/v1beta5"
	ptypes "pkg.akt.dev/go/node/provider/v1beta4"
	apclient "pkg.akt.dev/go/provider/client"

	cli "pkg.akt.dev/go/cli"
	ajwt "pkg.akt.dev/go/util/jwt"
	"pkg.akt.dev/node/app"
	cutils "pkg.akt.dev/node/x/cert/utils"
)

const (
	FlagService     = "service"
	flagOutput      = "output"
	flagFollow      = "follow"
	flagTail        = "tail"
	flagAuthType    = "auth-type"
	flagProviderURL = "provider-url"
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

func AddProviderOperationFlagsToCmd(cmd *cobra.Command) {
	cmd.Flags().String(flagProviderURL, "", "Provider URL for off-chain operations")
	cmd.Flags().Bool(cflags.FlagOffline, false, "Offline mode (does not allow any online functionality)")
}

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

func leasesForDeployment(ctx context.Context, cctx sdkclient.Context, flags *pflag.FlagSet, cl aclient.QueryClient) ([]mtypes.LeaseID, error) {
	var leases []mtypes.LeaseID
	var err error

	prov, err := flags.GetString(cflags.FlagProvider)
	if err != nil {
		return nil, err
	}
	var paddr sdk.AccAddress
	if prov != "" {
		paddr, err = sdk.AccAddressFromBech32(prov)
		if err != nil {
			return nil, err
		}
	}

	var dseq uint64
	var gseq uint32
	var oseq uint32

	owner := cctx.FromAddress

	dseq, err = flags.GetUint64(cflags.FlagDSeq)
	if err != nil {
		return nil, err
	}
	gseq, err = flags.GetUint32(cflags.FlagGSeq)
	if err != nil {
		return nil, err
	}
	oseq, err = flags.GetUint32(cflags.FlagOSeq)
	if err != nil {
		return nil, err
	}

	purl, _ := flags.GetString(cflags.FlagProviderURL)
	if purl == "" && cl != nil {
		filter := mtypes.LeaseFilters{
			Owner: owner.String(),
			DSeq:  dseq,
			State: mtypes.Lease_State_name[int32(mtypes.LeaseActive)],
		}

		resp, err := cl.Market().Leases(ctx, &mvbeta.QueryLeasesRequest{
			Filters: filter,
		})
		if err != nil {
			return nil, err
		}

		if len(resp.Leases) == 0 {
			return nil, fmt.Errorf("%w for dseq=%v", errNoActiveLease, dseq)
		}

		leases = make([]mtypes.LeaseID, 0, len(resp.Leases))

		for _, lease := range resp.Leases {
			leases = append(leases, lease.Lease.ID)
		}
	} else {
		leases = append(leases, mtypes.LeaseID{
			Owner:    owner.String(),
			DSeq:     dseq,
			GSeq:     gseq,
			OSeq:     oseq,
			Provider: paddr.String(),
		})
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

func setupProviderClient(ctx context.Context, cctx sdkclient.Context, flags *pflag.FlagSet, cl aclient.QueryClient, authRequired bool) (apclient.Client, error) {
	paddr, err := providerFromFlags(flags)
	if err != nil {
		return nil, err
	}

	purl, err := flags.GetString(flagProviderURL)
	if err != nil {
		return nil, err
	}

	if cl != nil && purl == "" {
		cl, err := discovery.DiscoverQueryClient(ctx, cctx)
		if err != nil {
			return nil, err
		}

		resp, err := cl.Provider().Provider(ctx, &ptypes.QueryProviderRequest{Owner: paddr.String()})
		if err != nil {
			return nil, err
		}

		purl = resp.Provider.HostURI
	}

	var opts []apclient.ClientOption

	if authRequired {
		opts, err = loadAuthOpts(ctx, cctx, flags)
		if err != nil {
			return nil, err
		}
	}

	if cl != nil {
		opts = append(opts, apclient.WithCertQuerier(pclient.NewCertificateQuerier(cl)))
	}

	// Always add the provider URL - the client requires it
	opts = append(opts, apclient.WithProviderURL(purl))

	pcl, err := apclient.NewClient(ctx, paddr, opts...)
	if err != nil {
		return nil, err
	}

	return pcl, nil
}

func ProviderPersistentPreRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	rpcURI, _ := cmd.Flags().GetString(cflags.FlagNode)
	if rpcURI != "" {
		ctx = context.WithValue(ctx, cli.ContextTypeRPCURI, rpcURI)
		cmd.SetContext(ctx)
	}

	cctx, err := cli.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	if cctx.Codec == nil {
		return errors.New("codec is not initialized")
	}

	if cctx.LegacyAmino == nil {
		return errors.New("legacy amino codec is not initialized")
	}

	if _, err = cli.ClientFromContext(ctx); err != nil {
		opts, err := cflags.ClientOptionsFromFlags(cmd.Flags())
		if err != nil {
			return err
		}

		var cl aclient.Client
		if !cctx.Offline {
			cl, err = discovery.DiscoverClient(ctx, cctx, opts...)
			if err != nil {
				return err
			}

		}

		ctx = context.WithValue(ctx, cli.ContextTypeClient, cl)

		cmd.SetContext(ctx)
	}

	return nil
}
