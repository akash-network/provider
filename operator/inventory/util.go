package inventory

import (
	"context"

	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"
	"k8s.io/client-go/informers"

	"github.com/akash-network/provider/tools/fromctx"
)

const (
	CtxKeyRookClientSet    = fromctx.Key("rook-clientset")
	CtxKeyStorage          = fromctx.Key("storage")
	CtxKeyFeatureDiscovery = fromctx.Key("feature-discovery")
	CtxKeyInformersFactory = fromctx.Key("informers-factory")
	CtxKeyHwInfo           = fromctx.Key("hardware-info")
	CtxKeyClusterState     = fromctx.Key("cluster-state")
	CtxKeyConfig           = fromctx.Key("config")
)

func InformersFactoryFromCtx(ctx context.Context) informers.SharedInformerFactory {
	val := ctx.Value(CtxKeyInformersFactory)
	if val == nil {
		panic("context does not have k8s factory set")
	}

	return val.(informers.SharedInformerFactory)
}

func RookClientFromCtx(ctx context.Context) *rookclientset.Clientset {
	val := ctx.Value(CtxKeyRookClientSet)
	if val == nil {
		panic("context does not have rook client set")
	}

	return val.(*rookclientset.Clientset)
}

func StorageFromCtx(ctx context.Context) []QuerierStorage {
	val := ctx.Value(CtxKeyStorage)
	if val == nil {
		panic("context does not have storage set")
	}

	return val.([]QuerierStorage)
}

func FeatureDiscoveryFromCtx(ctx context.Context) QuerierNodes {
	val := ctx.Value(CtxKeyFeatureDiscovery)
	if val == nil {
		panic("context does not have storage set")
	}

	return val.(QuerierNodes)
}

func ClusterStateFromCtx(ctx context.Context) QuerierCluster {
	val := ctx.Value(CtxKeyClusterState)
	if val == nil {
		panic("context does not have cluster state set")
	}

	return val.(QuerierCluster)
}

func ConfigFromCtx(ctx context.Context) Config {
	val := ctx.Value(CtxKeyConfig)
	if val == nil {
		panic("context does not have config set")
	}

	return val.(Config)
}
