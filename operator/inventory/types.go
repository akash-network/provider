package inventory

import (
	"context"
	"errors"

	rookexec "github.com/rook/rook/pkg/util/exec"
	"github.com/troian/pubsub"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	inventory "github.com/akash-network/akash-api/go/inventory/v1"

	"github.com/akash-network/provider/tools/fromctx"
)

const (
	FlagAPITimeout          = "api-timeout"
	FlagQueryTimeout        = "query-timeout"
	FlagRESTPort            = "rest-port"
	FlagGRPCPort            = "grpc-port"
	FlagPodName             = "pod-name"
	FlagPodNamespace        = "pod-namespace"
	FlagConfig              = "config"
	FlagProviderConfigsURL  = "provider-configs-url"
	FlagPciDbURL            = "provider-pcidb-url"
	FlagRegistryQueryPeriod = "registry-query-period"
	FlagDiscoveryImage      = "discovery-image"

	defaultProviderConfigsURL = "https://provider-configs.akash.network"
)

const (
	topicInventoryNode    = "inventory-node"
	topicInventoryNodes   = "inventory-nodes"
	topicInventoryStorage = "inventory-storage"
	topicInventoryConfig  = "inventory-config"
	topicInventoryCluster = "inventory-cluster"
	topicGPUIDs           = "gpu-ids"
	topicStorageClasses   = "storage-classes"
	topicKubeSC           = "kube-sc"
	topicKubeNS           = "kube-ns"
	topicKubeNodes        = "kube-nodes"
	topicKubeCephClusters = "kube-ceph-clusters"
	topicKubePV           = "kube-pv"
)

type dpReqType int

const (
	dpReqCPU dpReqType = iota
	dpReqGPU
	dpReqMem
)

type dpReadResp struct {
	data interface{}
	err  error
}
type dpReadReq struct {
	ctx  context.Context
	op   dpReqType
	resp chan<- dpReadResp
}

var (
	ErrMetricsUnsupportedRequest = errors.New("unsupported request method")
)

type storageClasses map[string]*storagev1.StorageClass

type storageSignal struct {
	driver  string
	storage inventory.ClusterStorage
}

type respNodes struct {
	res inventory.Nodes
	err error
}

type respCluster struct {
	res inventory.Cluster
	err error
}

type reqCluster struct {
	respCh chan respCluster
}

type reqNodes struct {
	respCh chan respNodes
}

type querierNodes struct {
	reqch chan reqNodes
}

type querierCluster struct {
	reqch chan reqCluster
}

func newQuerierCluster() querierCluster {
	return querierCluster{
		reqch: make(chan reqCluster, 100),
	}
}

func newQuerierNodes() querierNodes {
	return querierNodes{
		reqch: make(chan reqNodes, 100),
	}
}

func (c *querierCluster) Query(ctx context.Context) (inventory.Cluster, error) {
	r := reqCluster{
		respCh: make(chan respCluster, 1),
	}

	select {
	case c.reqch <- r:
	case <-ctx.Done():
		return inventory.Cluster{}, ctx.Err()
	}

	select {
	case rsp := <-r.respCh:
		return rsp.res, rsp.err
	case <-ctx.Done():
		return inventory.Cluster{}, ctx.Err()
	}
}

func (c *querierNodes) Query(ctx context.Context) (inventory.Nodes, error) {
	r := reqNodes{
		respCh: make(chan respNodes, 1),
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

type QuerierStorage interface{}

type QuerierCluster interface {
	Query(ctx context.Context) (inventory.Cluster, error)
}

type QuerierNodes interface {
	Query(ctx context.Context) (inventory.Nodes, error)
}

type Watcher interface {
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

type RemotePodCommandExecutor interface {
	ExecWithOptions(ctx context.Context, options rookexec.ExecOptions) (string, string, error)
	ExecCommandInContainerWithFullOutput(ctx context.Context, appLabel, containerName, namespace string, cmd ...string) (string, string, error)
	// ExecCommandInContainerWithFullOutputWithTimeout uses 15s hard-coded timeout
	ExecCommandInContainerWithFullOutputWithTimeout(ctx context.Context, appLabel, containerName, namespace string, cmd ...string) (string, string, error)
}

func NewRemotePodCommandExecutor(restcfg *rest.Config, clientset kubernetes.Interface) RemotePodCommandExecutor {
	return &rookexec.RemotePodCommandExecutor{
		ClientSet:  clientset,
		RestClient: restcfg,
	}
}

func InformKubeObjects(ctx context.Context, pub pubsub.Publisher, informer cache.SharedIndexInformer, topic string) {
	fromctx.MustErrGroupFromCtx(ctx).Go(func() error {
		_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pub.Pub(watch.Event{
					Type:   watch.Added,
					Object: obj.(runtime.Object),
				}, []string{topic})
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				pub.Pub(watch.Event{
					Type:   watch.Modified,
					Object: newObj.(runtime.Object),
				}, []string{topic})
			},
			DeleteFunc: func(obj interface{}) {
				pub.Pub(watch.Event{
					Type:   watch.Deleted,
					Object: obj.(runtime.Object),
				}, []string{topic})
			},
		})

		if err != nil {
			fromctx.LogrFromCtx(ctx).Error(err, "couldn't register event handlers")
			return nil
		}

		informer.Run(ctx.Done())

		return nil
	})
}

func (s storageClasses) copy() storageClasses {
	res := make(storageClasses)

	for name, sc := range s {
		res[name] = sc.DeepCopy()
	}

	return res
}
