package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	inventory "github.com/akash-network/akash-api/go/inventory/v1"
	"github.com/go-logr/logr"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	rookclientset "github.com/rook/rook/pkg/client/clientset/versioned"
	rookifactory "github.com/rook/rook/pkg/client/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/akash-network/provider/cluster/kube/builder"
	"github.com/akash-network/provider/tools/fromctx"
)

const (
	crdDiscoverPeriod = 30 * time.Second
	falseVal          = "false"
)

type stats struct {
	TotalBytes         uint64  `json:"total_bytes"`
	TotalAvailBytes    uint64  `json:"total_avail_bytes"`
	TotalUsedBytes     uint64  `json:"total_used_bytes"`
	TotalUsedRawBytes  uint64  `json:"total_user_raw_bytes"`
	TotalUsedRawRatio  float64 `json:"total_used_raw_ration"`
	NumOSDs            uint64  `json:"num_osds"`
	NumPerPoolOSDs     uint64  `json:"num_per_pool_osds"`
	NumPerPoolOmapOSDs uint64  `json:"num_per_pool_omap_osds"`
}

type statsClass struct {
	TotalBytes        uint64  `json:"total_bytes"`
	TotalAvailBytes   uint64  `json:"total_avail_bytes"`
	TotalUsedBytes    uint64  `json:"total_used_bytes"`
	TotalUsedRawBytes uint64  `json:"total_user_raw_bytes"`
	TotalUsedRawRatio float64 `json:"total_used_raw_ration"`
}

// poolStats response from *ceph df*
type poolStats struct {
	Name  string `json:"name"`
	ID    int    `json:"id"`
	Stats struct {
		Stored      uint64  `json:"stored"`
		Objects     uint64  `json:"objects"`
		KbUsed      uint64  `json:"kb_used"`
		BytesUsed   uint64  `json:"bytes_used"`
		PercentUsed float64 `json:"percent_used"`
		MaxAvail    uint64  `json:"max_avail"`
	} `json:"stats"`
}

// dfResp exec output from *ceph df*
type dfResp struct {
	Stats        stats                 `json:"stats"`
	StatsByClass map[string]statsClass `json:"stats_by_class"`
	Pools        []poolStats           `json:"pools"`
}

type cephClusters map[string]string

type cephStorageClass struct {
	isCeph         bool
	isAkashManaged bool
	pool           string
	clusterID      string
	allocated      *resource.Quantity
}

type cephStorageClasses map[string]*cephStorageClass

// nolint: unused
func (sc cephStorageClasses) dup() cephStorageClasses {
	res := make(cephStorageClasses, len(sc))

	for class := range sc {
		res[class] = sc[class]
	}

	return res
}

// nolint: unused
func (cc cephClusters) dup() cephClusters {
	res := make(cephClusters, len(cc))

	for id, ns := range cc {
		res[id] = ns
	}

	return res
}

type scrapeResp struct {
	clusters map[string]dfResp
	err      error
}

type scrapeReq struct {
	scs      cephStorageClasses
	clusters map[string]string
	respch   chan<- scrapeResp
}

type ceph struct {
	exe      RemotePodCommandExecutor
	ctx      context.Context
	cancel   context.CancelFunc
	scrapech chan scrapeReq
}

func NewCeph(ctx context.Context) (QuerierStorage, error) {
	ctx, cancel := context.WithCancel(ctx)

	c := &ceph{
		exe:      NewRemotePodCommandExecutor(fromctx.MustKubeConfigFromCtx(ctx), fromctx.MustKubeClientFromCtx(ctx)),
		ctx:      ctx,
		cancel:   cancel,
		scrapech: make(chan scrapeReq, 1),
	}

	startch := make(chan struct{}, 1)

	group := fromctx.MustErrGroupFromCtx(ctx)
	group.Go(func() error {
		return c.run(startch)
	})
	group.Go(c.scraper)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-startch:
	}

	return c, nil
}

func (c *ceph) crdInstalled(log logr.Logger, rc *rookclientset.Clientset) bool {
	groups, err := rc.Discovery().ServerGroups()
	if err != nil {
		log.Error(err, "discover server groups")
		return false
	}

	for _, group := range groups.Groups {
		if group.Name == "ceph.rook.io" {
			return true
		}
	}

	return false
}

func (c *ceph) run(startch chan<- struct{}) error {
	bus := fromctx.MustPubSubFromCtx(c.ctx)

	events := bus.Sub(topicKubeNS, topicKubeSC, topicKubePV, topicKubeCephClusters)

	defer bus.Unsub(events)

	log := fromctx.LogrFromCtx(c.ctx).WithName("rook-ceph")

	clusters := make(cephClusters)
	scs := make(cephStorageClasses)

	rc := RookClientFromCtx(c.ctx)
	kc := fromctx.MustKubeClientFromCtx(c.ctx)

	factory := rookifactory.NewSharedInformerFactory(rc, 0)
	informer := factory.Ceph().V1().CephClusters().Informer()

	pvMap := make(map[string]corev1.PersistentVolume)

	crdDiscoverTick := time.NewTimer(1 * time.Second)

	scrapecnt := 0
	scrapeRespch := make(chan scrapeResp, 1)
	scrapech := c.scrapech

	startch <- struct{}{}

	signalScrape := func() {
		select {
		case scrapech <- scrapeReq{
			scs:      scs,
			clusters: clusters.dup(),
			respch:   scrapeRespch,
		}:
			scrapech = nil
			scrapecnt = 0
		default:
			scrapecnt++
		}
	}

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-crdDiscoverTick.C:
			if c.crdInstalled(log, rc) {
				crdDiscoverTick.Stop()
				InformKubeObjects(c.ctx,
					bus,
					informer,
					topicKubeCephClusters)
			} else {
				crdDiscoverTick.Reset(crdDiscoverPeriod)
			}
		case rawEvt := <-events:
			switch evt := rawEvt.(type) {
			case watch.Event:
				if evt.Object == nil {
					break
				}

				kind := reflect.TypeOf(evt.Object).String()
				if idx := strings.LastIndex(kind, "."); idx > 0 {
					kind = kind[idx+1:]
				}
				msg := fmt.Sprintf("%8s monitoring %s", evt.Type, kind)

			evtdone:
				switch obj := evt.Object.(type) {
				case *storagev1.StorageClass:
					switch evt.Type {
					case watch.Added:
						fallthrough
					case watch.Modified:
						lblVal := obj.Labels[builder.AkashManagedLabelName]
						if lblVal == "" {
							lblVal = falseVal
						}

						sc := scs[obj.Name]
						if sc == nil {
							sc = &cephStorageClass{
								allocated: resource.NewQuantity(0, resource.DecimalSI),
							}

							scs[obj.Name] = sc
						}

						sc.isCeph = strings.HasSuffix(obj.Provisioner, ".csi.ceph.com")
						sc.pool = obj.Parameters["pool"]
						sc.clusterID = obj.Parameters["clusterID"]
						sc.isAkashManaged, _ = strconv.ParseBool(lblVal)

						if sc.isCeph {
							if sc.pool == "" {
								log.Info("StorageClass does not have \"pool\" parameter set", "StorageClass", obj.Name)
							}

							if sc.clusterID == "" {
								log.Info("StorageClass does not have \"clusterID\" parameter set", "StorageClass", obj.Name)
							}
						}

					case watch.Deleted:
						delete(scs, obj.Name)
					default:
						break evtdone
					}

					log.Info(msg, "name", obj.Name)
					signalScrape()
				case *rookv1.CephCluster:
					switch evt.Type {
					case watch.Added:
						fallthrough
					case watch.Modified:
						// add only clusters in with State == Created
						if obj.Status.State == rookv1.ClusterStateCreated {
							clusters[obj.Name] = obj.Namespace
							log.Info(msg, "ns", obj.Namespace, "name", obj.Name)
						}
					case watch.Deleted:
						log.Info(msg, "ns", obj.Namespace, "name", obj.Name)
						delete(clusters, obj.Name)
					}
					signalScrape()
				case *corev1.PersistentVolume:
					switch evt.Type {
					case watch.Added:
						fallthrough
					case watch.Modified:
						capacity, exists := obj.Spec.Capacity[corev1.ResourceStorage]
						if !exists {
							break
						}

						sc, exists := scs[obj.Spec.StorageClassName]
						if !exists {
							scItem, _ := kc.StorageV1().StorageClasses().Get(c.ctx, obj.Spec.StorageClassName, metav1.GetOptions{})

							lblVal := scItem.Labels[builder.AkashManagedLabelName]
							if lblVal == "" {
								lblVal = falseVal
							}

							sc = &cephStorageClass{
								isCeph:    strings.HasSuffix(scItem.Provisioner, ".csi.ceph.com"),
								pool:      scItem.Parameters["pool"],
								clusterID: scItem.Parameters["clusterID"],
								allocated: resource.NewQuantity(0, resource.DecimalSI),
							}

							sc.isAkashManaged, _ = strconv.ParseBool(lblVal)

							scs[scItem.Name] = sc
						}

						if _, exists = pvMap[obj.Name]; !exists {
							pvMap[obj.Name] = *obj
							sc.allocated.Add(capacity)
						}
					case watch.Deleted:
						res, exists := obj.Spec.Capacity[corev1.ResourceStorage]
						if !exists {
							break
						}

						delete(pvMap, obj.Name)

						scs[obj.Spec.StorageClassName].allocated.Sub(res)
					}
					signalScrape()
				}
			}
		case res := <-scrapeRespch:

			if len(res.clusters) > 0 {
				var result inventory.ClusterStorage

				for class, params := range scs {
					if !params.isCeph {
						continue
					}

					df, exists := res.clusters[params.clusterID]
					if !exists || !params.isAkashManaged {
						continue
					}

					for _, pool := range df.Pools {
						if pool.Name == params.pool {
							allocated := params.allocated.DeepCopy()
							result = append(result, inventory.Storage{
								Quantity: inventory.ResourcePair{
									Allocated:   &allocated,
									Allocatable: resource.NewQuantity(int64(pool.Stats.MaxAvail), resource.DecimalSI), // nolint: gosec
									Capacity:    resource.NewQuantity(int64(pool.Stats.MaxAvail), resource.DecimalSI), // nolint: gosec
								},
								Info: inventory.StorageInfo{
									Class: class,
								},
							})
							break
						}
					}
				}

				if len(result) > 0 {
					bus.Pub(storageSignal{
						driver:  "ceph",
						storage: result,
					}, []string{topicInventoryStorage})
				}
			}

			scrapech = c.scrapech

			if scrapecnt > 0 {
				signalScrape()
			}
		}
	}
}

func (c *ceph) scraper() error {
	log := fromctx.LogrFromCtx(c.ctx).WithName("rook-ceph")

	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case req := <-c.scrapech:
			dfResults := make(map[string]dfResp)
			for clusterID, ns := range req.clusters {
				stdout, _, err := c.exe.ExecCommandInContainerWithFullOutputWithTimeout(c.ctx, "rook-ceph-tools", "rook-ceph-tools", ns, "ceph", "df", "--format", "json")
				if err != nil {
					log.Error(err, "unable to scrape ceph metrics")
				}

				rsp := dfResp{}

				_ = json.Unmarshal([]byte(stdout), &rsp)

				dfResults[clusterID] = rsp
			}

			req.respch <- scrapeResp{
				clusters: dfResults,
				err:      nil,
			}
		}
	}
}
