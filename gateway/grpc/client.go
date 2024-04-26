package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Client struct {
	providerv1.ProviderRPCClient
	leasev1.LeaseRPCClient

	conn *grpc.ClientConn
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func NewClient(ctx context.Context, addr string, cert tls.Certificate, cquery ctypes.QueryClient) (*Client, error) {
	tlsConfig := tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{cert},
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(chain [][]byte, _ [][]*x509.Certificate) error {
			if len(chain) == 0 {
				return errors.New("tls: empty chain")
			}

			if len(chain) > 1 {
				return errors.New("tls: invalid certificate chain")
			}

			c, err := x509.ParseCertificate(chain[0])
			if err != nil {
				return fmt.Errorf("x509 parse certificate: %w", err)
			}

			// validation
			owner, err := sdk.AccAddressFromBech32(c.Subject.CommonName)
			if err != nil {
				return fmt.Errorf("tls: invalid certificate's subject common name: %w", err)
			}

			// 1. CommonName in issuer and Subject must match and be as Bech32 format
			if c.Subject.CommonName != c.Issuer.CommonName {
				return fmt.Errorf("tls: invalid certificate's issuer common name: %w", err)
			}

			// 2. serial number must be in
			if c.SerialNumber == nil {
				return fmt.Errorf("tls: invalid certificate serial number: %w", err)
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
				return fmt.Errorf("tls: unable to fetch certificate from chain: %w", err)
			}
			if (len(resp.Certificates) != 1) || !resp.Certificates[0].Certificate.IsState(ctypes.CertificateValid) {
				return fmt.Errorf("tls: attempt to use non-existing or revoked certificate: %w", err)
			}

			clientCertPool := x509.NewCertPool()
			clientCertPool.AddCert(c)

			opts := x509.VerifyOptions{
				Roots:                     clientCertPool,
				CurrentTime:               time.Now(),
				KeyUsages:                 []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				MaxConstraintComparisions: 0,
			}

			if _, err = c.Verify(opts); err != nil {
				return fmt.Errorf("tls: unable to verify certificate: %w", err)
			}

			return nil
		},
	}

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithBlock(),
		grpc.WithTransportCredentials(credentials.NewTLS(&tlsConfig)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial context %s: %w", addr, err)
	}

	return &Client{
		ProviderRPCClient: providerv1.NewProviderRPCClient(conn),
		LeaseRPCClient:    leasev1.NewLeaseRPCClient(conn),

		conn: conn,
	}, nil
}
