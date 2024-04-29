package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
)

func NewServerTLSConfig(ctx context.Context, certs []tls.Certificate, cquery ctypes.QueryClient) (*tls.Config, error) {
	// InsecureSkipVerify is set to true due to inability to use normal TLS verification
	// certificate validation and authentication performed in VerifyPeerCertificate
	cfg := &tls.Config{
		Certificates:       certs,
		ClientAuth:         tls.RequestClientCert,
		InsecureSkipVerify: true, // nolint: gosec
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(certificates [][]byte, _ [][]*x509.Certificate) error {
			if len(certificates) > 0 {
				if len(certificates) != 1 {
					return fmt.Errorf("tls: invalid certificate chain")
				}

				cert, err := x509.ParseCertificate(certificates[0])
				if err != nil {
					return fmt.Errorf("%w: tls: failed to parse certificate", err)
				}

				// validation
				var owner sdk.Address
				if owner, err = sdk.AccAddressFromBech32(cert.Subject.CommonName); err != nil {
					return fmt.Errorf("%w: tls: invalid certificate's subject common name", err)
				}

				// 1. CommonName in issuer and Subject must match and be as Bech32 format
				if cert.Subject.CommonName != cert.Issuer.CommonName {
					return fmt.Errorf("%w: tls: invalid certificate's issuer common name", err)
				}

				// 2. serial number must be in
				if cert.SerialNumber == nil {
					return fmt.Errorf("%w: tls: invalid certificate serial number", err)
				}

				// 3. look up certificate on chain
				var resp *ctypes.QueryCertificatesResponse
				resp, err = cquery.Certificates(
					ctx,
					&ctypes.QueryCertificatesRequest{
						Filter: ctypes.CertificateFilter{
							Owner:  owner.String(),
							Serial: cert.SerialNumber.String(),
							State:  "valid",
						},
					},
				)
				if err != nil {
					return fmt.Errorf("%w: tls: unable to fetch certificate from chain", err)
				}
				if (len(resp.Certificates) != 1) || !resp.Certificates[0].Certificate.IsState(ctypes.CertificateValid) {
					return fmt.Errorf("%w tls: attempt to use non-existing or revoked certificate", err)
				}

				block, rest := pem.Decode(resp.Certificates[0].Certificate.Cert)
				if len(rest) > 0 {
					return fmt.Errorf("%w: tls: failed to decode onchain certificate", err)
				}

				onchainCert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return fmt.Errorf("%w: tls: failed to parse onchain certificate", err)
				}

				clientCertPool := x509.NewCertPool()
				clientCertPool.AddCert(onchainCert)

				opts := x509.VerifyOptions{
					Roots:                     clientCertPool,
					CurrentTime:               time.Now(),
					KeyUsages:                 []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
					MaxConstraintComparisions: 0,
				}

				if _, err = cert.Verify(opts); err != nil {
					return fmt.Errorf("%w: tls: unable to verify certificate", err)
				}
			}
			return nil
		},
	}

	return cfg, nil
}
