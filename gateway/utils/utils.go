package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	atls "github.com/akash-network/akash-api/go/util/tls"
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
				peerCerts := make([]*x509.Certificate, 0, len(certificates))

				for idx := range certificates {
					cert, err := x509.ParseCertificate(certificates[idx])
					if err != nil {
						return err
					}

					peerCerts = append(peerCerts, cert)
				}

				_, _, err := atls.ValidatePeerCertificates(ctx, cquery, peerCerts, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
				if err != nil {
					return err
				}
			}
			return nil
		},
	}

	return cfg, nil
}
