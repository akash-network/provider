package grpc

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	providerv1 "github.com/akash-network/akash-api/go/provider/v1"
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
		certsServer = make([]tls.Certificate, 1)
		err         error
	)

	certsServer[0], err = tls.LoadX509KeyPair("testdata/localhost_1.crt", "testdata/localhost_1.key")
	require.NoError(t, err)

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

			statusClient := c.statusClient()
			statusClient.AssertExpectations(t)

			s := newServer(ctx, certsServer, statusClient)
			defer s.Stop()

			l, err := net.Listen("tcp", ":0")
			require.NoError(t, err)

			go func() {
				require.NoError(t, s.Serve(l))
			}()

			cert, err := tls.LoadX509KeyPair("testdata/localhost_2.crt", "testdata/localhost_2.key")
			require.NoError(t, err)

			tlsConfig := tls.Config{
				Certificates: []tls.Certificate{cert},
			}

			conn, err := grpc.Dial(l.Addr().String(),
				grpc.WithTransportCredentials(credentials.NewTLS(&tlsConfig)))
			require.NoError(t, err)

			defer conn.Close()

			c.run(ctx, t, providerv1.NewProviderRPCClient(conn))
		})
	}
}
