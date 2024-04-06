package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	leasev1 "github.com/akash-network/akash-api/go/provider/lease/v1"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"

	"github.com/akash-network/provider/gateway/utils"
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
		VerifyPeerCertificate: func(certificates [][]byte, _ [][]*x509.Certificate) error {
			if _, err := utils.VerifyOwnerCertBytes(ctx, certificates, "", x509.ExtKeyUsageClientAuth, cquery); err != nil {
				return err
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
