package cmd

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"
	"pkg.akt.dev/go/cli"
	cflags "pkg.akt.dev/go/cli/flags"
)

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

// attestationQuoteRequest matches the sidecar's QuoteRequest type.
type attestationQuoteRequest struct {
	Nonce   string `json:"nonce"`
	BindTLS bool   `json:"bind_tls,omitempty"`
}

// attestationQuoteResponse matches the sidecar's QuoteResponse type.
type attestationQuoteResponse struct {
	Report    string `json:"report"`
	CertChain string `json:"cert_chain"`
	TEEType   string `json:"tee_type"`
	AuxBlob   string `json:"auxblob"`
	TLSBound  bool   `json:"tls_bound"`
}

// attestationResult is the CLI output format.
type attestationResult struct {
	Nonce         string                   `json:"nonce"`
	Quote         attestationQuoteResponse `json:"quote"`
	ReportSize    int                      `json:"report_size_bytes"`
	NonceVerified bool                     `json:"nonce_verified"`
	MockReport    bool                     `json:"mock_report"`
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

	gclient, err := setupProviderClient(ctx, cctx, cmd.Flags(), queryClientOrNil(cl), paddr, true)
	if err != nil {
		return err
	}

	// Generate a random 64-byte nonce
	var nonce [64]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	nonceB64 := base64.StdEncoding.EncodeToString(nonce[:])

	// Build the quote request
	reqBody, err := json.Marshal(attestationQuoteRequest{
		Nonce: nonceB64,
	})
	if err != nil {
		return fmt.Errorf("marshal quote request: %w", err)
	}

	// Call the provider's attestation quote endpoint (authenticated, same as lease-status)
	respBody, err := gclient.AttestationQuote(ctx, bid.LeaseID(), reqBody)
	if err != nil {
		return showErrorToUser(err)
	}

	// Parse the response
	var quote attestationQuoteResponse
	if err := json.Unmarshal(respBody, &quote); err != nil {
		return fmt.Errorf("parse quote response: %w", err)
	}

	// Decode report to check size and verify nonce
	reportBytes, err := base64.StdEncoding.DecodeString(quote.Report)
	if err != nil {
		return fmt.Errorf("decode report: %w", err)
	}

	// Check if this is a mock report (starts with "MOCK")
	isMock := len(reportBytes) >= 4 && string(reportBytes[0:4]) == "MOCK"

	// Verify nonce echo in report_data
	nonceVerified := false
	if isMock && len(reportBytes) >= 144 {
		// Mock report: nonce at offset 80
		nonceVerified = true
		for i := 0; i < 64; i++ {
			if reportBytes[80+i] != nonce[i] {
				nonceVerified = false
				break
			}
		}
	} else if !isMock && len(reportBytes) >= 0x90 {
		// Real SNP report: REPORT_DATA at offset 0x50 (80 decimal)
		nonceVerified = true
		for i := 0; i < 64; i++ {
			if reportBytes[0x50+i] != nonce[i] {
				nonceVerified = false
				break
			}
		}
	}

	result := attestationResult{
		Nonce:         nonceB64,
		Quote:         quote,
		ReportSize:    len(reportBytes),
		NonceVerified: nonceVerified,
		MockReport:    isMock,
	}

	return cli.PrintJSON(cctx, result)
}
