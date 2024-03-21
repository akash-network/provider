package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/pkg/errors"

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
			if _, err := VerifyOwnerCert(ctx, certificates, "", x509.ExtKeyUsageClientAuth, cquery); err != nil {
				return err
			}
			return nil
		},
	}

	return cfg, nil
}

type cert interface {
	*x509.Certificate | []byte
}

func VerifyOwnerCert[T cert](
	ctx context.Context,
	chain []T,
	dnsName string,
	usage x509.ExtKeyUsage,
	cquery ctypes.QueryClient,
) (sdk.Address, error) {
	if len(chain) == 0 {
		return nil, nil
	}

	if len(chain) > 1 {
		return nil, errors.Errorf("tls: invalid certificate chain")
	}

	var c *x509.Certificate

	switch t := any(chain).(type) {
	case []*x509.Certificate:
		c = t[0]
	case [][]byte:
		var err error
		if c, err = x509.ParseCertificate(t[0]); err != nil {
			return nil, fmt.Errorf("tls: failed to parse certificate: %w", err)
		}
	}

	// validation
	owner, err := sdk.AccAddressFromBech32(c.Subject.CommonName)
	if err != nil {
		return nil, fmt.Errorf("tls: invalid certificate's subject common name: %w", err)
	}

	// 1. CommonName in issuer and Subject must match and be as Bech32 format
	if c.Subject.CommonName != c.Issuer.CommonName {
		return nil, fmt.Errorf("tls: invalid certificate's issuer common name: %w", err)
	}

	// 2. serial number must be in
	if c.SerialNumber == nil {
		return nil, fmt.Errorf("tls: invalid certificate serial number: %w", err)
	}

	// 3. look up certificate on chain
	var resp *ctypes.QueryCertificatesResponse
	resp, err = cquery.Certificates(
		ctx,
		&ctypes.QueryCertificatesRequest{
			Filter: ctypes.CertificateFilter{
				Owner:  owner.String(),
				Serial: c.SerialNumber.String(),
				State:  "valid",
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("tls: unable to fetch certificate from chain: %w", err)
	}
	if (len(resp.Certificates) != 1) || !resp.Certificates[0].Certificate.IsState(ctypes.CertificateValid) {
		return nil, fmt.Errorf("tls: attempt to use non-existing or revoked certificate: %w", err)
	}

	clientCertPool := x509.NewCertPool()
	clientCertPool.AddCert(c)

	opts := x509.VerifyOptions{
		DNSName:                   dnsName,
		Roots:                     clientCertPool,
		CurrentTime:               time.Now(),
		KeyUsages:                 []x509.ExtKeyUsage{usage},
		MaxConstraintComparisions: 0,
	}

	if _, err = c.Verify(opts); err != nil {
		return nil, fmt.Errorf("tls: unable to verify certificate: %w", err)
	}

	return owner, nil
}
