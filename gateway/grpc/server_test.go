package grpc

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	qmock "github.com/akash-network/akash-api/go/node/client/v1beta2/mocks"
	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
	"github.com/akash-network/node/testutil"
	"github.com/akash-network/provider/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRPCs(t *testing.T) {
	var (
		qclient qmock.QueryClient
		com     = testutil.CertificateOptionMocks(&qclient)
		cod     = testutil.CertificateOptionDomains([]string{"localhost", "127.0.0.1"})
	)

	var (
		crt1 = testutil.Certificate(t, testutil.AccAddress(t), com, cod)
		crt2 = testutil.Certificate(t, testutil.AccAddress(t), com, cod)
	)

	cases := []struct {
		desc         string
		statusClient func() *mocks.StatusClient
		run          func(context.Context, *testing.T, providerv1.ProviderRPCClient)
	}{
		{
			desc: "GetStatus",
			statusClient: func() *mocks.StatusClient {
				var m mocks.StatusClient
				m.EXPECT().StatusV1(mock.AnythingOfType("context.Context")).Return(nil, nil)
				return &m
			},
			run: func(ctx context.Context, t *testing.T, c providerv1.ProviderRPCClient) {
				_, err := c.GetStatus(ctx, &emptypb.Empty{})
				assert.NoError(t, err)
			},
		},
	}

	for _, c := range cases {
		c := c

		t.Run(c.desc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ctx = context.WithValue(ctx, ContextKeyQueryClient, &qclient)

			statusClient := c.statusClient()
			statusClient.AssertExpectations(t)

			s := newServer(ctx, crt1.Cert, statusClient, &qclient)
			defer s.Stop()

			l, err := net.Listen("tcp", ":0")
			require.NoError(t, err)

			go func() {
				require.NoError(t, s.Serve(l))
			}()

			tlsConfig := tls.Config{
				InsecureSkipVerify: true,
				Certificates:       crt2.Cert,
			}

			conn, err := grpc.DialContext(ctx, l.Addr().String(),
				grpc.WithTransportCredentials(credentials.NewTLS(&tlsConfig)))
			require.NoError(t, err)

			defer conn.Close()

			c.run(ctx, t, providerv1.NewProviderRPCClient(conn))
		})
	}
}
