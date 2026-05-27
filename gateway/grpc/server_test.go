package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	grpcpkg "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	ajwt "pkg.akt.dev/go/util/jwt"
)

func TestAuthInterceptorAllowsNoAuthorizationHeader(t *testing.T) {
	interceptor := authInterceptor()
	handlerCalled := false

	resp, err := interceptor(context.Background(), nil, &grpcpkg.UnaryServerInfo{}, func(ctx context.Context, _ interface{}) (interface{}, error) {
		handlerCalled = true
		require.Equal(t, ajwt.AccessTypeNone, ClaimsFromCtx(ctx).Leases.Access)

		return "ok", nil
	})

	require.NoError(t, err)
	require.Equal(t, "ok", resp)
	require.True(t, handlerCalled)
}

func TestAuthInterceptorSingleAuthorizationHeaderDoesNotPanic(t *testing.T) {
	interceptor := authInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer not-a-jwt"))
	handlerCalled := false
	var err error

	require.NotPanics(t, func() {
		_, err = interceptor(ctx, nil, &grpcpkg.UnaryServerInfo{}, func(context.Context, interface{}) (interface{}, error) {
			handlerCalled = true

			return nil, nil
		})
	})

	require.Error(t, err)
	require.False(t, handlerCalled)
}
