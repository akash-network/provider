package fromctx

import (
	"context"

	"github.com/akash-network/provider/cluster/types/v1beta3/clients/hostname"
	"github.com/akash-network/provider/cluster/types/v1beta3/clients/inventory"
	"github.com/akash-network/provider/cluster/types/v1beta3/clients/ip"
)

type CtxKey string

const (
	CtxKeyClientIP        = CtxKey("client-ip")
	CtxKeyClientHostname  = CtxKey("client-hostname")
	CtxKeyClientInventory = CtxKey("client-inventory")
)

func ClientIPFromContext(ctx context.Context) ip.Client {
	var res ip.Client

	val := ctx.Value(CtxKeyClientIP)
	if val == nil {
		return nil
	}

	res = val.(ip.Client)
	return res
}

func ClientHostnameFromContext(ctx context.Context) hostname.Client {
	var res hostname.Client

	val := ctx.Value(CtxKeyClientHostname)
	if val == nil {
		return res
	}

	res = val.(hostname.Client)
	return res
}

func ClientInventoryFromContext(ctx context.Context) inventory.Client {
	var res inventory.Client

	val := ctx.Value(CtxKeyClientInventory)
	if val == nil {
		return res
	}

	res = val.(inventory.Client)
	return res
}
