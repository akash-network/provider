package inventory

import (
	"context"

	"github.com/boz/go-lifecycle"
	"github.com/cskr/pubsub"
	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"

	akashclientset "github.com/ovrclk/provider-services/pkg/client/clientset/versioned"
)

func LogFromCtx(ctx context.Context) logr.Logger {
	lg, _ := logr.FromContext(ctx)
	return lg
}

func KubeConfigFromCtx(ctx context.Context) *rest.Config {
	val := ctx.Value(CtxKeyKubeConfig)
	if val == nil {
		panic("context does not have kubeconfig set")
	}

	return val.(*rest.Config)
}

func KubeClientFromCtx(ctx context.Context) *kubernetes.Clientset {
	val := ctx.Value(CtxKeyKubeClientSet)
	if val == nil {
		panic("context does not have kube client set")
	}

	return val.(*kubernetes.Clientset)
}

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

func AkashClientFromCtx(ctx context.Context) *akashclientset.Clientset {
	val := ctx.Value(CtxKeyAkashClientSet)
	if val == nil {
		panic("context does not have akash client set")
	}

	return val.(*akashclientset.Clientset)
}

func PubSubFromCtx(ctx context.Context) *pubsub.PubSub {
	val := ctx.Value(CtxKeyPubSub)
	if val == nil {
		panic("context does not have pubsub set")
	}

	return val.(*pubsub.PubSub)
}

func LifecycleFromCtx(ctx context.Context) lifecycle.Lifecycle {
	val := ctx.Value(CtxKeyLifecycle)
	if val == nil {
		panic("context does not have lifecycle set")
	}

	return val.(lifecycle.Lifecycle)
}

func ErrGroupFromCtx(ctx context.Context) *errgroup.Group {
	val := ctx.Value(CtxKeyErrGroup)
	if val == nil {
		panic("context does not have errgroup set")
	}

	return val.(*errgroup.Group)
}

func StorageFromCtx(ctx context.Context) []Storage {
	val := ctx.Value(CtxKeyStorage)
	if val == nil {
		panic("context does not have storage set")
	}

	return val.([]Storage)
}
