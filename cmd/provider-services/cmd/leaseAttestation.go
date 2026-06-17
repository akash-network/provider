package cmd

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"
	leasev1 "pkg.akt.dev/go/provider/lease/v1"
	ajwt "pkg.akt.dev/go/util/jwt"
)

const grpcPort = "8444"

func leaseAttestationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "lease-attestation",
		Short:        "request attestation quote from a confidential compute lease",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(0),
		PreRunE:      ProviderPersistentPreRunE,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doLeaseAttestation(cmd)
		},
	}

	AddProviderOperationFlagsToCmd(cmd)
	addLeaseFlags(cmd)
	addAuthFlags(cmd)

	return cmd
}

// attestationResult is the CLI output format.
type attestationResult struct {
	Nonce         string                        `json:"nonce"`
	Quote         *leasev1.AttestationQuoteResponse `json:"quote"`
	ReportSize    int                           `json:"report_size_bytes"`
	NonceVerified bool                          `json:"nonce_verified"`
	MockReport    bool                          `json:"mock_report"`
}

func doLeaseAttestation(cmd *cobra.Command) error {
	ctx := cmd.Context()
	cl, err := cli.ClientFromContext(ctx)
	if err != nil && !errors.Is(err, cli.ErrContextValueNotSet) {
		return err
	}
	cctx, err := cli.GetClientTxContext(cmd)
	if err != nil {
		return err
	}

	bid, err := cflags.BidIDFromFlags(cmd.Flags(), cflags.WithOwner(cctx.FromAddress))
	if err != nil {
		return err
	}

	paddr, err := sdk.AccAddressFromBech32(bid.Provider)
	if err != nil {
		return err
	}

	leaseID := bid.LeaseID()

	// Resolve provider URL (REST host), then derive the gRPC address.
	purl, err := resolveProviderURL(ctx, cctx, cmd.Flags(), queryClientOrNil(cl), paddr)
	if err != nil {
		return err
	}

	grpcAddr, err := grpcAddrFromProviderURL(purl)
	if err != nil {
		return err
	}

	// Generate JWT for authentication.
	signer := ajwt.NewSigner(cctx.Keyring, cctx.FromAddress)
	token, err := generateJWT(signer)
	if err != nil {
		return fmt.Errorf("generate JWT: %w", err)
	}

	// Dial the gRPC server with TLS (provider uses self-signed certs).
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, // nolint: gosec
	}
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close() // nolint: errcheck

	// Generate a random 64-byte nonce.
	var nonce [64]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	nonceB64 := base64.StdEncoding.EncodeToString(nonce[:])

	// Call AttestationQuote via gRPC with JWT in metadata.
	rpcClient := leasev1.NewLeaseRPCClient(conn)
	md := metadata.Pairs("authorization", "Bearer "+token)
	rpcCtx := metadata.NewOutgoingContext(ctx, md)

	resp, err := rpcClient.AttestationQuote(rpcCtx, &leasev1.AttestationQuoteRequest{
		LeaseId: leaseID,
		Nonce:   nonceB64,
	})
	if err != nil {
		return showErrorToUser(err)
	}

	// Decode report to check size and verify nonce.
	reportBytes, err := base64.StdEncoding.DecodeString(resp.GetReport())
	if err != nil {
		return fmt.Errorf("decode report: %w", err)
	}

	// Check if this is a mock report (starts with "MOCK").
	isMock := len(reportBytes) >= 4 && string(reportBytes[0:4]) == "MOCK"

	// Verify nonce echo in report_data.
	nonceVerified := false
	if isMock && len(reportBytes) >= 144 {
		nonceVerified = bytesEqual(reportBytes[80:144], nonce[:])
	} else if !isMock {
		// Try TDX Quote v4 first (report_data at header + body offset 520).
		const tdxReportDataOffset = 48 + 520
		if len(reportBytes) >= tdxReportDataOffset+64 {
			nonceVerified = bytesEqual(reportBytes[tdxReportDataOffset:tdxReportDataOffset+64], nonce[:])
		}
		// Fall back to SNP report_data offset.
		if !nonceVerified && len(reportBytes) >= 0x90 {
			nonceVerified = bytesEqual(reportBytes[0x50:0x50+64], nonce[:])
		}
	}

	result := attestationResult{
		Nonce:         nonceB64,
		Quote:         resp,
		ReportSize:    len(reportBytes),
		NonceVerified: nonceVerified,
		MockReport:    isMock,
	}

	return cli.PrintJSON(cctx, result)
}

// grpcAddrFromProviderURL takes the provider's REST URL and returns host:8444 for gRPC.
func grpcAddrFromProviderURL(u *url.URL) (string, error) {
	hostname := u.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("empty hostname in provider URL %q", u.String())
	}
	return hostname + ":" + grpcPort, nil
}

// generateJWT creates a signed JWT token for gRPC auth, replicating the chain-sdk client's newJWT().
func generateJWT(signer ajwt.SignerI) (string, error) {
	now := time.Now()

	claims := ajwt.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    signer.GetAddress().String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
		},
		Version: "v1",
		Leases:  ajwt.Leases{Access: ajwt.AccessTypeFull},
	}

	tok := jwt.NewWithClaims(ajwt.SigningMethodES256K, &claims)

	return tok.SignedString(signer)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
