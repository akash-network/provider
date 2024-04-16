package grpc

import (
	"context"

	ctypes "github.com/akash-network/akash-api/go/node/cert/v1beta3"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type ContextKey string

const (
	ContextKeyQueryClient = ContextKey("query-client")
	ContextKeyOwner       = ContextKey("owner")
)

func ContextWithQueryClient(ctx context.Context, c ctypes.QueryClient) context.Context {
	return context.WithValue(ctx, ContextKeyQueryClient, c)
}

func MustQueryClientFromCtx(ctx context.Context) ctypes.QueryClient {
	val := ctx.Value(ContextKeyQueryClient)
	if val == nil {
		panic("context does not have query client set")
	}

	return val.(ctypes.QueryClient)
}

func ContextWithOwner(ctx context.Context, address sdk.Address) context.Context {
	return context.WithValue(ctx, ContextKeyOwner, address)
}

func OwnerFromCtx(ctx context.Context) sdk.Address {
	val := ctx.Value(ContextKeyOwner)
	if val == nil {
		return sdk.AccAddress{}
	}

	return val.(sdk.Address)
}
