package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	atls "github.com/akash-network/akash-api/go/util/tls"
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

			cert, err := x509.ParseCertificate(chain[0])
			if err != nil {
				return fmt.Errorf("x509 parse certificate: %w", err)
			}

			_, _, err = atls.ValidatePeerCertificates(
				ctx,
				cquery,
				[]*x509.Certificate{cert},
				[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
			if err != nil {
				return fmt.Errorf("validate peer certificates: %w", err)
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
