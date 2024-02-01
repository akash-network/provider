package inventory

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/troian/pubsub"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	inventory "github.com/akash-network/akash-api/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

type rancher struct {
	exe    RemotePodCommandExecutor
	ctx    context.Context
	cancel context.CancelFunc
}

type rancherStorage struct {
	isRancher      bool
	isAkashManaged bool
	allocated      uint64
}

type rancherStorageClasses map[string]*rancherStorage

func NewRancher(ctx context.Context) (QuerierStorage, error) {
	ctx, cancel := context.WithCancel(ctx)

	r := &rancher{
		exe:    NewRemotePodCommandExecutor(fromctx.KubeConfigFromCtx(ctx), fromctx.KubeClientFromCtx(ctx)),
		ctx:    ctx,
		cancel: cancel,
	}

	startch := make(chan struct{}, 1)

	group := fromctx.ErrGroupFromCtx(ctx)
	group.Go(func() error {
		return r.run(startch)
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-startch:
	}

	return r, nil
}

func (c *rancher) run(startch chan<- struct{}) error {
	defer func() {
		c.cancel()
	}()

	bus := fromctx.PubSubFromCtx(c.ctx)

	var unsubChs []<-chan interface{}

	var pvWatcher watch.Interface
	var pvEvents <-chan watch.Event

	defer func() {
		if pvWatcher != nil {
			pvWatcher.Stop()
		}

		for _, ch := range unsubChs {
			bus.Unsub(ch)
		}
	}()

	events := bus.Sub(topicKubeNS, topicKubeSC, topicKubeNodes)

	log := fromctx.LogrFromCtx(c.ctx).WithName("rancher")

	scs := make(rancherStorageClasses)

	allocatable := int64(math.MaxInt64)

	pvMap := make(map[string]corev1.PersistentVolume)

	scrapeCh := make(chan struct{}, 1)
	scrapech := scrapeCh

	startch <- struct{}{}

	tryScrape := func() {
		select {
		case scrapech <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case evt := <-pvEvents:
			// nolint: gocritic
			switch obj := evt.Object.(type) {
			case *corev1.PersistentVolume:
				switch evt.Type {
				case watch.Added:
					fallthrough
				case watch.Modified:
					res, exists := obj.Spec.Capacity[corev1.ResourceStorage]
					if !exists {
						break
					}

					params, exists := scs[obj.Spec.StorageClassName]
					if !exists {
						scItem, _ := fromctx.KubeClientFromCtx(c.ctx).StorageV1().StorageClasses().Get(c.ctx, obj.Spec.StorageClassName, metav1.GetOptions{})

						lblVal := scItem.Labels[builder.AkashManagedLabelName]
						if lblVal == "" {
							lblVal = falseVal
						}

						params = &rancherStorage{
							isRancher: scItem.Provisioner == "rancher.io/local-path",
						}

						params.isAkashManaged, _ = strconv.ParseBool(lblVal)

						scs[obj.Spec.StorageClassName] = params
					}

					if _, exists = pvMap[obj.Name]; !exists {
						pvMap[obj.Name] = *obj
						params.allocated += uint64(res.Value())
					}
				case watch.Deleted:
					res, exists := obj.Spec.Capacity[corev1.ResourceStorage]
					if !exists {
						break
					}

					delete(pvMap, obj.Name)
					scs[obj.Spec.StorageClassName].allocated -= uint64(res.Value())
				}

				tryScrape()
			}
		case rawEvt := <-events:
			switch evt := rawEvt.(type) {
			case watch.Event:
				kind := reflect.TypeOf(evt.Object).String()
				if idx := strings.LastIndex(kind, "."); idx > 0 {
					kind = kind[idx+1:]
				}
				msg := fmt.Sprintf("%8s monitoring %s", evt.Type, kind)

			evtdone:
				switch obj := evt.Object.(type) {
				case *corev1.Node:
					if val, exists := obj.Status.Allocatable[corev1.ResourceEphemeralStorage]; exists {
						allocatable = val.Value()
					}
				case *storagev1.StorageClass:
					switch evt.Type {
					case watch.Added:
						fallthrough
					case watch.Modified:
						lblVal := obj.Labels[builder.AkashManagedLabelName]
						if lblVal == "" {
							lblVal = falseVal
						}

						sc, exists := scs[obj.Name]
						if !exists {
							sc = &rancherStorage{}
						}

						sc.isRancher = obj.Provisioner == "rancher.io/local-path"
						sc.isAkashManaged, _ = strconv.ParseBool(lblVal)
						scs[obj.Name] = sc

						scList, _ := fromctx.KubeClientFromCtx(c.ctx).StorageV1().StorageClasses().List(c.ctx, metav1.ListOptions{})
						if len(scList.Items) == len(scs) && pvWatcher == nil {
							var err error
							pvWatcher, err = fromctx.KubeClientFromCtx(c.ctx).CoreV1().PersistentVolumes().Watch(c.ctx, metav1.ListOptions{})
							if err != nil {
								log.Error(err, "couldn't start watcher on persistent volumes")
							}
							pvEvents = pvWatcher.ResultChan()

							pvList, err := fromctx.KubeClientFromCtx(c.ctx).CoreV1().PersistentVolumes().List(c.ctx, metav1.ListOptions{})
							if err != nil {
								log.Error(err, "couldn't list persistent volumes")
							}

							for _, pv := range pvList.Items {
								capacity, exists := pv.Spec.Capacity[corev1.ResourceStorage]
								if !exists {
									continue
								}

								params := scs[pv.Spec.StorageClassName]
								params.allocated += uint64(capacity.Value())

								pvMap[pv.Name] = pv
							}
						}

					case watch.Deleted:
						// volumes can remain without storage class so to keep metrics right when storage class suddenly
						// recreated we don't delete it
					default:
						break evtdone
					}

					log.Info(msg, "name", obj.Name)
				default:
					break evtdone
				}
			}
			tryScrape()
		case <-scrapech:
			var res inventory.ClusterStorage

			for class, params := range scs {
				if params.isRancher && params.isAkashManaged {
					res = append(res, inventory.Storage{
						Quantity: inventory.NewResourcePair(allocatable, int64(params.allocated), resource.DecimalSI),
						Info: inventory.StorageInfo{
							Class: class,
						},
					})
				}
			}

			if len(res) > 0 {
				bus.Pub(storageSignal{
					driver:  "rancher",
					storage: res,
				}, []string{topicInventoryStorage}, pubsub.WithRetain())
			}

			scrapech = scrapeCh
		}
	}
}
