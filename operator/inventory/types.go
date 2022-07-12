package inventory

import (
	"context"

	"github.com/cskr/pubsub"

	rookexec "github.com/rook/rook/pkg/util/exec"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	providerflags "github.com/ovrclk/provider-services/cmd/provider-services/cmd/flags"
	akashv2beta1 "github.com/ovrclk/provider-services/pkg/apis/akash.network/v2beta1"
)

const (
	FlagAPITimeout   = "api-timeout"
	FlagQueryTimeout = "query-timeout"
	FlagAPIPort      = "api-port"
)

type ContextKey string

const (
	CtxKeyKubeConfig       = ContextKey(providerflags.FlagKubeConfig)
	CtxKeyKubeClientSet    = ContextKey("kube-clientset")
	CtxKeyRookClientSet    = ContextKey("rook-clientset")
	CtxKeyAkashClientSet   = ContextKey("akash-clientset")
	CtxKeyPubSub           = ContextKey("pubsub")
	CtxKeyLifecycle        = ContextKey("lifecycle")
	CtxKeyErrGroup         = ContextKey("errgroup")
	CtxKeyStorage          = ContextKey("storage")
	CtxKeyInformersFactory = ContextKey("informers-factory")
)

type resp struct {
	res []akashv2beta1.InventoryClusterStorage
	err error
}

type req struct {
	respCh chan resp
}

type querier struct {
	reqch chan req
}

func newQuerier() querier {
	return querier{
		reqch: make(chan req, 100),
	}
}

func (c *querier) Query(ctx context.Context) ([]akashv2beta1.InventoryClusterStorage, error) {
	r := req{
		respCh: make(chan resp, 1),
	}

	select {
	case c.reqch <- r:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case rsp := <-r.respCh:
		return rsp.res, rsp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type Storage interface {
	Query(ctx context.Context) ([]akashv2beta1.InventoryClusterStorage, error)
}

type Watcher interface {
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

type RemotePodCommandExecutor interface {
	ExecWithOptions(options rookexec.ExecOptions) (string, string, error)
	ExecCommandInContainerWithFullOutput(ctx context.Context, appLabel, containerName, namespace string, cmd ...string) (string, string, error)
	// ExecCommandInContainerWithFullOutputWithTimeout uses 15s hard-coded timeout
	ExecCommandInContainerWithFullOutputWithTimeout(ctx context.Context, appLabel, containerName, namespace string, cmd ...string) (string, string, error)
}

func NewRemotePodCommandExecutor(restcfg *rest.Config, clientset *kubernetes.Clientset) RemotePodCommandExecutor {
	return &rookexec.RemotePodCommandExecutor{
		ClientSet:  clientset,
		RestClient: restcfg,
	}
}

func InformKubeObjects(ctx context.Context, pubsub *pubsub.PubSub, informer cache.SharedIndexInformer, topic string) {
	ErrGroupFromCtx(ctx).Go(func() error {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pubsub.Pub(watch.Event{
					Type:   watch.Added,
					Object: obj.(runtime.Object),
				}, topic)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				pubsub.Pub(watch.Event{
					Type:   watch.Modified,
					Object: newObj.(runtime.Object),
				}, topic)
			},
			DeleteFunc: func(obj interface{}) {
				pubsub.Pub(watch.Event{
					Type:   watch.Deleted,
					Object: obj.(runtime.Object),
				}, topic)
			},
		})

		informer.Run(ctx.Done())
		return nil
	})
}
