package inventory

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	akashv2beta1 "github.com/ovrclk/provider-services/pkg/apis/akash.network/v2beta1"
)

type rancher struct {
	exe    RemotePodCommandExecutor
	ctx    context.Context
	cancel context.CancelFunc
	querier
}

type rancherStorage struct {
	isRancher      bool
	isAkashManaged bool
	allocated      uint64
}

type rancherStorageClasses map[string]*rancherStorage

func NewRancher(ctx context.Context) (Storage, error) {
	ctx, cancel := context.WithCancel(ctx)

	r := &rancher{
		exe:     NewRemotePodCommandExecutor(KubeConfigFromCtx(ctx), KubeClientFromCtx(ctx)),
		ctx:     ctx,
		cancel:  cancel,
		querier: newQuerier(),
	}

	group := ErrGroupFromCtx(ctx)
	group.Go(r.run)

	return r, nil
}

func (c *rancher) run() error {
	defer func() {
		c.cancel()
	}()

	events := make(chan interface{}, 1000)

	pubsub := PubSubFromCtx(c.ctx)

	defer pubsub.Unsub(events)
	pubsub.AddSub(events, "ns", "sc", "pv", "nodes")

	log := LogFromCtx(c.ctx).WithName("rancher")

	scs := make(rancherStorageClasses)

	resources := akashv2beta1.ResourcePair{
		Allocatable: math.MaxUint64,
	}

	scSynced := false
	pvSynced := false

	pvCount := 0

	var pendingPVs []watch.Event

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case rawEvt := <-events:
			if scSynced && len(pendingPVs) > 0 {
				select {
				case events <- pendingPVs[0]:
					pendingPVs = pendingPVs[1:]
				default:
				}
			}
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
					if allocatable, exists := obj.Status.Allocatable[corev1.ResourceEphemeralStorage]; exists {
						resources.Allocatable = uint64(allocatable.Value())
					}
				case *storagev1.StorageClass:
					switch evt.Type {
					case watch.Added:
						fallthrough
					case watch.Modified:
						lblVal := obj.Labels["akash.network"]
						if lblVal == "" {
							lblVal = "false"
						}

						sc, exists := scs[obj.Name]

						if !exists {
							sc = &rancherStorage{
								isRancher: obj.Provisioner == "rancher.io/local-path",
							}
						}

						sc.isAkashManaged, _ = strconv.ParseBool(lblVal)
						scs[obj.Name] = sc

						scList, _ := KubeClientFromCtx(c.ctx).StorageV1().StorageClasses().List(c.ctx, metav1.ListOptions{})
						if len(scList.Items) == len(scs) && !scSynced {
							scSynced = true

							if len(pendingPVs) > 0 {
								select {
								case events <- pendingPVs[0]:
									pendingPVs = pendingPVs[1:]
								default:
								}
							}

							pvList, _ := KubeClientFromCtx(c.ctx).CoreV1().PersistentVolumes().List(c.ctx, metav1.ListOptions{})
							if len(pvList.Items) == pvCount && !pvSynced {
								pvSynced = true
							}
						}
					case watch.Deleted:
						// volumes can remain without storage class so to keep metrics wight when storage class suddenly
						// recreated we don't delete it
					default:
						break evtdone
					}

					log.Info(msg, "name", obj.Name)
				case *corev1.PersistentVolume:
					switch evt.Type {
					case watch.Added:
						pvCount++
						fallthrough
					case watch.Modified:
						resource, exists := obj.Spec.Capacity[corev1.ResourceStorage]
						if !exists {
							break
						}

						if params, exists := scs[obj.Name]; !exists {
							scs[obj.Spec.StorageClassName] = &rancherStorage{
								allocated: uint64(resource.Value()),
							}
						} else {
							params.allocated += uint64(resource.Value())
						}

						pvList, _ := KubeClientFromCtx(c.ctx).CoreV1().PersistentVolumes().List(c.ctx, metav1.ListOptions{})
						if len(pvList.Items) == pvCount && !pvSynced {
							pvSynced = true
						}
					case watch.Deleted:
						pvCount--
						resource, exists := obj.Spec.Capacity[corev1.ResourceStorage]
						if !exists {
							break
						}

						scs[obj.Spec.StorageClassName].allocated -= uint64(resource.Value())
					default:
						break evtdone
					}
					log.Info(msg, "name", obj.Name)
				default:
					break evtdone
				}
			}
		case req := <-c.reqch:
			var resp resp

			if pvSynced {
				var res []akashv2beta1.InventoryClusterStorage

				for class, params := range scs {
					if params.isRancher && params.isAkashManaged {
						res = append(res, akashv2beta1.InventoryClusterStorage{
							Class: class,
							ResourcePair: akashv2beta1.ResourcePair{
								Allocated:   params.allocated,
								Allocatable: resources.Allocatable,
							},
						})
					}
				}

				resp.res = res
			} else {
				resp.err = errors.New("rancher inventory is being updated")
			}

			req.respCh <- resp
		}
	}
}
