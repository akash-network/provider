package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/jaypipes/ghw/pkg/pci"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	v1 "pkg.akt.dev/go/inventory/v1"

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/tools/fromctx"
)

var (
	errWorkerExit = errors.New("worker finished")

	labelNvidiaComGPUPresent = fmt.Sprintf("%s.present", builder.ResourceGPUNvidia)
)

type k8sPatch struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

type nodeDiscovery struct {
	ctx       context.Context
	cancel    context.CancelFunc
	group     *errgroup.Group
	kc        kubernetes.Interface
	readch    chan dpReadReq
	readych   chan struct{}
	sig       chan<- string
	name      string
	namespace string
	image     string
}

func newNodeDiscovery(ctx context.Context, name, namespace string, image string, sig chan<- string) *nodeDiscovery {
	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	nd := &nodeDiscovery{
		ctx:       ctx,
		cancel:    cancel,
		group:     group,
		kc:        fromctx.MustKubeClientFromCtx(ctx),
		readch:    make(chan dpReadReq, 1),
		readych:   make(chan struct{}),
		sig:       sig,
		name:      name,
		namespace: namespace,
		image:     image,
	}

	group.Go(nd.apiConnector)
	group.Go(nd.monitor)

	return nd
}

// hostPathDirectoryPtr returns a pointer to HostPathDirectory for the
// /sys/class mount the IB discovery handler walks. We require the parent
// to exist (it always does on any Linux host — sysfs is mounted by the
// kernel) and let the handler tolerate a missing infiniband subdir on
// non-interconnect nodes. DirectoryOrCreate is not an option here because sysfs
// is read-only and any mkdir against /sys/* fails — that mode is what
// caused operator-inventory-hardware-discovery to hang in
// ContainerCreating in CI on the initial interconnect PR.
func hostPathDirectoryPtr() *corev1.HostPathType {
	t := corev1.HostPathDirectory
	return &t
}

func (dp *nodeDiscovery) shutdown() error {
	dp.cancel()

	return dp.group.Wait()
}

func (dp *nodeDiscovery) queryCPU(ctx context.Context) (*cpu.Info, error) {
	respch := make(chan dpReadResp, 1)

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case <-rctx.Done():
		return nil, rctx.Err()
	case dp.readch <- dpReadReq{
		ctx:  rctx,
		op:   dpReqCPU,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		if resp.data == nil {
			return nil, resp.err
		}
		return resp.data.(*cpu.Info), resp.err
	}
}

// queryIB asks the per-node psutil pod to walk /sys/class/infiniband and
// return the host's HCA prefix + fabric. A zero-valued result means the
// node has no interconnect hardware (or the DaemonSet pod is missing the sysfs
// mount); callers treat it as "this node has no interconnect capability."
func (dp *nodeDiscovery) queryIB(ctx context.Context) (*IBDiscovery, error) {
	respch := make(chan dpReadResp, 1)

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case <-rctx.Done():
		return nil, rctx.Err()
	case dp.readch <- dpReadReq{
		ctx:  rctx,
		op:   dpReqIB,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		if resp.data == nil {
			return nil, resp.err
		}
		return resp.data.(*IBDiscovery), resp.err
	}
}

// queryPCI asks the per-node psutil pod for the host's PCI bus enumeration.
// Used as a fallback when ghw's `/sys/class/drm` walk misses compute GPUs
// (headless A100/H100 nodes where the NVIDIA driver lives inside the GPU
// Operator container, not on the host kernel — so no DRM nodes for the
// compute cards, only the BMC VGA).
func (dp *nodeDiscovery) queryPCI(ctx context.Context) (*pci.Info, error) {
	respch := make(chan dpReadResp, 1)

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case <-rctx.Done():
		return nil, rctx.Err()
	case dp.readch <- dpReadReq{
		ctx:  rctx,
		op:   dpReqPCI,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		if resp.data == nil {
			return nil, resp.err
		}
		return resp.data.(*pci.Info), resp.err
	}
}

func (dp *nodeDiscovery) queryGPU(ctx context.Context) (*gpu.Info, error) {
	respch := make(chan dpReadResp, 1)

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case <-rctx.Done():
		return nil, rctx.Err()
	case dp.readch <- dpReadReq{
		ctx:  rctx,
		op:   dpReqGPU,
		resp: respch,
	}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		return nil, dp.ctx.Err()
	case resp := <-respch:
		if resp.data == nil {
			return nil, resp.err
		}
		return resp.data.(*gpu.Info), resp.err
	}
}

func (dp *nodeDiscovery) apiConnector() error {
	ctx := dp.ctx

	log := fromctx.LogrFromCtx(ctx).WithName("node.discovery")

	defer func() {
		log.Info("shutting down hardware discovery pod", "node", dp.name)
		dp.sig <- dp.name
	}()

	log.Info("starting hardware discovery pod", "node", dp.name)

	apiPort := 8081

	gracePeriod := int64(1)
	name := fmt.Sprintf("operator-inventory-hardware-discovery-%s", dp.name)
	req := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: dp.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "inventory",
				"app.kubernetes.io/instance":  "inventory-hardware-discovery",
				"app.kubernetes.io/component": "operator",
				"app.kubernetes.io/part-of":   "provider",
			},
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: &gracePeriod,
			NodeName:                      dp.name,
			ServiceAccountName:            "operator-inventory-hardware-discovery",
			Containers: []corev1.Container{
				{
					Name:  "psutil",
					Image: dp.image,
					Args: []string{
						"provider-services",
						"tools",
						"psutil",
						"serve",
						fmt.Sprintf("--api-port=%d", apiPort),
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "api",
							ContainerPort: 8081,
						},
					},
					Env: []corev1.EnvVar{
						{
							Name:  "PCIDB_ENABLE_NETWORK_FETCH",
							Value: "1",
						},
					},
					// P-1: mount the host's /sys so the /infiniband handler
					// can walk /host/sys/class/infiniband AND resolve the
					// sysfs symlinks each device dir points into under
					// /sys/devices/.../infiniband/<dev>. Mounting only
					// /sys/class makes the directory listing work (the
					// names are stored in the parent) but every read of a
					// device subdir (ports/link_layer, etc) goes through
					// the symlink target and lands outside the mount.
					// Sysfs is read-only so a full /sys mount is safe;
					// HostPathDirectory because /sys always exists.
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "host-sys",
							MountPath: "/host/sys",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "host-sys",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/sys",
							Type: hostPathDirectoryPtr(),
						},
					},
				},
			},
		},
	}

	kc := fromctx.MustKubeClientFromCtx(ctx)

	var pod *corev1.Pod
	var err error

	for {
		pod, err = kc.CoreV1().Pods(dp.namespace).Create(ctx, req, metav1.CreateOptions{})
		if err == nil {
			break
		}

		if errors.Is(err, context.Canceled) {
			return err
		}

		if !kerrors.IsAlreadyExists(err) {
			log.Error(err, fmt.Sprintf("unable to start discovery pod on node \"%s\"", dp.name))
		}

		tctx, tcancel := context.WithTimeout(ctx, time.Second)
		<-tctx.Done()

		tcancel()
		if !errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return tctx.Err()
		}
	}

	defer func() {
		// using default context here to delete pod as main might have been canceled
		_ = kc.CoreV1().Pods(dp.namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	}()

	watcher, err := kc.CoreV1().Pods(dp.namespace).Watch(dp.ctx, metav1.ListOptions{
		Watch:           true,
		ResourceVersion: pod.ResourceVersion,
		FieldSelector: fields.Set{
			"metadata.name": pod.Name,
			"spec.nodeName": pod.Spec.NodeName}.AsSelector().String(),
		LabelSelector: "app.kubernetes.io/name=inventory" +
			",app.kubernetes.io/instance=inventory-hardware-discovery" +
			",app.kubernetes.io/component=operator" +
			",app.kubernetes.io/part-of=provider",
	})
	if err != nil {
		log.Error(err, fmt.Sprintf("unable to start pod watcher on node \"%s\"", dp.name))
		return err
	}

	defer func() {
		watcher.Stop()
	}()

initloop:
	for {
		select {
		case <-dp.ctx.Done():
			return dp.ctx.Err()
		case evt, isopen := <-watcher.ResultChan():
			if !isopen {
				return errWorkerExit
			}
			resp := evt.Object.(*corev1.Pod)
			if resp.Status.Phase == corev1.PodRunning {
				watcher.Stop()
				break initloop
			}
		}
	}

	log.Info("started hardware discovery pod", "node", dp.name)

	dp.readych <- struct{}{}

	for {
		select {
		case <-dp.ctx.Done():
			return dp.ctx.Err()
		case readreq := <-dp.readch:
			var res string
			resp := dpReadResp{}

			switch readreq.op {
			case dpReqCPU:
				res = "cpu"
			case dpReqGPU:
				res = "gpu"
			case dpReqMem:
				res = "memory"
			case dpReqIB:
				res = "infiniband"
			case dpReqPCI:
				res = "pci"
			}

			result := kc.CoreV1().RESTClient().Get().
				Namespace(dp.namespace).
				Resource("pods").
				Name(fmt.Sprintf("%s:%d", pod.Name, apiPort)).
				SubResource("proxy").
				Suffix(res).
				Do(readreq.ctx)

			resp.err = result.Error()

			if resp.err == nil {
				var data []byte
				data, resp.err = result.Raw()
				if resp.err == nil {
					switch readreq.op {
					case dpReqCPU:
						var res cpu.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqGPU:
						var res gpu.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqMem:
						var res memory.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqIB:
						var res IBDiscovery
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					case dpReqPCI:
						var res pci.Info
						resp.err = json.Unmarshal(data, &res)
						resp.data = &res
					}
				}
			}

			readreq.resp <- resp
		}
	}
}

func isPodAllocated(status corev1.PodStatus) bool {
	for _, condition := range status.Conditions {
		if condition.Type == corev1.PodScheduled {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func (dp *nodeDiscovery) monitor() error {
	ctx := dp.ctx
	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	bus := fromctx.MustPubSubFromCtx(ctx)
	kc := fromctx.MustKubeClientFromCtx(ctx)

	log.Info("starting", "node", dp.name)

	nodesch := bus.Sub(topicKubeNodes)
	cfgch := bus.Sub(topicInventoryConfig)
	idsch := bus.Sub(topicGPUIDs)
	scch := bus.Sub(topicStorageClasses)

	defer func() {
		log.Info("shutting down monitor", "node", dp.name)

		bus.Unsub(nodesch)
		bus.Unsub(idsch)
		bus.Unsub(cfgch)
		bus.Unsub(scch)
	}()

	var podsWatch watch.Interface
	var cfg Config
	var sc storageClasses

	lastPubState := nodeStateRemoved

	gpusIDs := make(RegistryGPUVendors)
	currLabels := make(map[string]string)
	currPods := make(map[string]corev1.Pod)

	select {
	case <-dp.ctx.Done():
		return dp.ctx.Err()
	case <-dp.readych:
	}

	select {
	case <-dp.ctx.Done():
		return dp.ctx.Err()
	case evt := <-cfgch:
		cfg = evt.(Config)
	}

	select {
	case <-dp.ctx.Done():
		return dp.ctx.Err()
	case evt := <-scch:
		sc = evt.(storageClasses)
	}

	select {
	case evt := <-idsch:
		gpusIDs = evt.(RegistryGPUVendors)
	default:
	}

	knode, err := dp.kc.CoreV1().Nodes().Get(ctx, dp.name, metav1.GetOptions{})
	if err == nil {
		currLabels = copyManagedLabels(knode.Labels)
	}

	node := dp.initNodeInfo(gpusIDs, knode, cfg.Interconnect.ResourcePatterns)

	restartPodsWatcher := func() error {
		if podsWatch != nil {
			select {
			case <-podsWatch.ResultChan():
			default:
			}
		}

		var terr error
		podsWatch, terr = kc.CoreV1().Pods(corev1.NamespaceAll).Watch(dp.ctx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("spec.nodeName", dp.name).String(),
		})

		if terr != nil {
			log.Error(terr, "unable to start pods watcher")
			return terr
		}

		pods, terr := kc.CoreV1().Pods(corev1.NamespaceAll).List(dp.ctx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("spec.nodeName", dp.name).String(),
		})
		if terr != nil {
			return terr
		}

		nodeResetAllocated(&node)

		currPods = make(map[string]corev1.Pod)

		for idx := range pods.Items {
			pod := pods.Items[idx].DeepCopy()

			if !isPodAllocated(pod.Status) {
				continue
			}

			addPodAllocatedResources(&node, pod)

			currPods[pod.Name] = *pod
		}

		return nil
	}

	err = restartPodsWatcher()
	if err != nil {
		return err
	}

	defer func() {
		if podsWatch != nil {
			podsWatch.Stop()
		}
	}()

	statech := make(chan struct{}, 1)
	labelch := make(chan struct{}, 1)

	signalState := func() {
		select {
		case statech <- struct{}{}:
		default:
		}
	}

	signalLabels := func() {
		select {
		case labelch <- struct{}{}:
		default:
		}
	}

	defer func() {
		if lastPubState != nodeStateRemoved {
			bus.Pub(nodeState{
				state: nodeStateRemoved,
				name:  dp.name,
			}, []string{topicInventoryNode})
		}
	}()

	log.Info("started", "node", dp.name)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt := <-cfgch:
			cfg = evt.(Config)
			signalLabels()
		case evt := <-scch:
			sc = evt.(storageClasses)
			signalLabels()
		case evt := <-idsch:
			gpusIDs = evt.(RegistryGPUVendors)
			node.Resources.GPU.Info = dp.parseGPUInfo(ctx, gpusIDs)
			signalLabels()
		case rEvt := <-nodesch:
			evt := rEvt.(watch.Event)
			switch obj := evt.Object.(type) {
			case *corev1.Node:
				if obj.Name == dp.name {
					switch evt.Type {
					case watch.Modified:
						if nodeAllocatableChanged(knode, obj) {
							updateNodeInfo(obj, &node, cfg.Interconnect.ResourcePatterns)
							if err = restartPodsWatcher(); err != nil {
								return err
							}
						}

						signalLabels()
					}

					knode = obj.DeepCopy()
				}
			}
		case res, isopen := <-podsWatch.ResultChan():
			if !isopen {
				podsWatch.Stop()
				if err = restartPodsWatcher(); err != nil {
					return err
				}

				continue
			}

			obj := res.Object.(*corev1.Pod)
			switch res.Type {
			case watch.Added:
				fallthrough
			case watch.Modified:
				if _, exists := currPods[obj.Name]; !exists && isPodAllocated(obj.Status) {
					currPods[obj.Name] = *obj.DeepCopy()
					addPodAllocatedResources(&node, obj)
				}
			case watch.Deleted:
				pod, exists := currPods[obj.Name]
				if !exists {
					log.Info("received pod delete event for item that does not exist. check node inventory logic, it's might have bug in it!")
					break
				}

				subPodAllocatedResources(&node, &pod)

				delete(currPods, obj.Name)
			}
			signalState()
		case <-statech:
			if len(currLabels) > 0 {
				bus.Pub(nodeState{
					state: nodeStateUpdated,
					name:  dp.name,
					node:  node.Dup(),
				}, []string{topicInventoryNode})
				lastPubState = nodeStateUpdated
			} else if len(currLabels) == 0 && lastPubState != nodeStateRemoved {
				bus.Pub(nodeState{
					state: nodeStateRemoved,
					name:  dp.name,
				}, []string{topicInventoryNode})

				lastPubState = nodeStateRemoved
			}
		case <-labelch:
			labels, nNode := generateLabels(cfg, knode, node.Dup(), sc)
			if !reflect.DeepEqual(&nNode, &node) {
				node = nNode
				signalState()
			}

			if !reflect.DeepEqual(labels, currLabels) {
				currLabels = copyManagedLabels(labels)

				for key, val := range removeManagedLabels(knode.Labels) {
					labels[key] = val
				}

				patches := []k8sPatch{
					{
						Op:    "add",
						Path:  "/metadata/labels",
						Value: labels,
					},
				}

				data, _ := json.Marshal(patches)

				_, err := dp.kc.CoreV1().Nodes().Patch(dp.ctx, node.Name, k8stypes.JSONPatchType, data, metav1.PatchOptions{})
				if err != nil {
					log.Error(err, fmt.Sprintf("couldn't apply patches for node \"%s\"", node.Name))
				} else {
					log.Info(fmt.Sprintf("successfully applied labels and/or annotations patches for node \"%s\"", node.Name), "labels", currLabels)
				}

				signalState()
			}
		}
	}
}

func nodeAllocatableChanged(prev *corev1.Node, curr *corev1.Node) bool {
	changed := len(prev.Status.Allocatable) != len(curr.Status.Allocatable)

	if !changed {
		for pres, pval := range prev.Status.Allocatable {
			cval, exists := curr.Status.Allocatable[pres]
			if !exists || (pval.Value() != cval.Value()) {
				changed = true
				break
			}
		}
	}

	return changed
}

func (dp *nodeDiscovery) initNodeInfo(gpusIDs RegistryGPUVendors, knode *corev1.Node, interconnectPatterns []string) v1.Node {
	cpuInfo := dp.parseCPUInfo(dp.ctx)
	gpuInfo := dp.parseGPUInfo(dp.ctx, gpusIDs)
	ibInfo := dp.parseIBInfo(dp.ctx)

	res := v1.Node{
		Name: knode.Name,
		Resources: v1.NodeResources{
			CPU: v1.CPU{
				Quantity: v1.NewResourcePairMilli(0, 0, 0, resource.DecimalSI),
				Info:     cpuInfo,
			},
			GPU: v1.GPU{
				Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				Info:     gpuInfo,
			},
			Memory: v1.Memory{
				Quantity: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
				Info:     nil,
			},
			EphemeralStorage: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesAttached:  v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			VolumesMounted:   v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
			// GPU interconnect capacity is filled in by updateNodeInfo when the kubelet
			// publishes an rdma/rdma_shared_device_* extended resource.
			GPUInterconnect: v1.NewResourcePair(0, 0, 0, resource.DecimalSI),
		},
		Capabilities: v1.NodeCapabilities{
			// HCA prefixes + fabric come from the per-node sysfs probe via
			// the psutil pod. Empty on non-interconnect nodes.
			NCCLHCAPrefixes:    append([]string(nil), ibInfo.NCCLIBHCAPrefixes...),
			InterconnectFabric: ibInfo.Fabric,
		},
	}

	updateNodeInfo(knode, &res, interconnectPatterns)

	return res
}

// parseIBInfo asks the per-node psutil pod for the host's InfiniBand /
// RoCE inventory. A failure is logged but never fatal — the result is a
// zero-value IBDiscovery, which propagates through the rest of the
// inventory as "this node has no interconnect capability."
func (dp *nodeDiscovery) parseIBInfo(ctx context.Context) IBDiscovery {
	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	ib, err := dp.queryIB(ctx)
	if err != nil || ib == nil {
		if err != nil {
			log.V(4).Info("unable to query infiniband; treating node as non-interconnect", "err", err.Error())
		}
		return IBDiscovery{}
	}
	return *ib
}

// DefaultInterconnectResourcePattern matches the
// `rdma/rdma_shared_device_*` extended resources the Mellanox / NVIDIA
// k8s-rdma-shared-device-plugin publishes (e.g.
// `rdma/rdma_shared_device_ib` for InfiniBand-pinned plugins,
// `rdma/rdma_shared_device_eth` for RoCE). Used when the operator's
// `interconnect.resource_patterns` config list is empty so the rc4
// hardcoded behavior keeps working out of the box.
//
// AKT-493: an operator running a non-Mellanox plugin (Broadcom bnxt
// RDMA, Intel E810 iwarp, etc.) can override this with one or more
// patterns under `interconnect.resource_patterns` in provider.yaml.
const DefaultInterconnectResourcePattern = "rdma/rdma_shared_device_*"

// matchInterconnectResourceName returns the first extended-resource name
// from `quantities` that matches any of `patterns`, or empty if no
// pattern matches. Patterns use filepath.Match's shell-style globbing
// (`*`, `?`, character classes); a bare string with no glob metacharacter
// matches as a literal prefix (preserves the rc4 contract — empty
// `patterns` falls back to the Mellanox/NVIDIA default). The first hit
// wins; mixed fabrics on a single node are explicitly outside spec scope
// (§7.1).
func matchInterconnectResourceName(quantities corev1.ResourceList, patterns []string) corev1.ResourceName {
	effective := patterns
	if len(effective) == 0 {
		effective = []string{DefaultInterconnectResourcePattern}
	}
	// Iterate patterns first (preserves operator's config order — the
	// first pattern in `interconnect.resource_patterns` wins) and
	// resources second (kubelet ResourceList is a map with
	// non-deterministic iteration order). Without this ordering, the
	// "first match wins" guarantee would silently depend on Go's hash
	// seed.
	for _, p := range effective {
		for name := range quantities {
			if resourcePatternMatches(p, string(name)) {
				return name
			}
		}
	}
	return ""
}

// resourcePatternMatches applies one pattern to one kubelet resource
// name. Bare strings (no `*`, `?`, or `[`) match as a literal prefix —
// the rc4 contract. Patterns containing glob metacharacters use
// filepath.Match. A malformed pattern is treated as a non-match (the
// inventory operator surfaces config-load errors elsewhere; here we
// fail-closed rather than swallow the whole node).
func resourcePatternMatches(pattern, name string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.HasPrefix(name, pattern)
	}
	ok, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return ok
}

func updateNodeInfo(knode *corev1.Node, node *v1.Node, interconnectPatterns []string) {
	// Resolve whichever interconnect extended resource the cluster's
	// device plugin advertises. Empty `interconnectPatterns` falls back
	// to the rc4 hardcoded Mellanox/NVIDIA prefix; operators on other
	// plugins point at `broadcom.com/rdma`, `intel.com/iwarp_shared`,
	// etc. via the `interconnect.resource_patterns` config. If no
	// pattern matches, the name stays empty and the inventory client
	// treats the node as having no interconnect capability.
	//
	// Clear the interconnect fields up-front so a node whose
	// device-plugin resource was removed (or whose pattern config no
	// longer matches the previously-published name) properly drops to
	// non-interconnect rather than retaining the stale name + zeroed
	// capacity. The subsequent kubelet loops only set capacity/
	// allocatable when a match is found, so without this reset a
	// previously-set value would persist forever.
	node.Capabilities.InterconnectResourceName = ""
	node.Resources.GPUInterconnect.Capacity.Set(0)
	node.Resources.GPUInterconnect.Allocatable.Set(0)

	interconnectResource := matchInterconnectResourceName(knode.Status.Allocatable, interconnectPatterns)
	if interconnectResource == "" {
		interconnectResource = matchInterconnectResourceName(knode.Status.Capacity, interconnectPatterns)
	}
	if interconnectResource != "" {
		node.Capabilities.InterconnectResourceName = string(interconnectResource)
	}

	for name, r := range knode.Status.Allocatable {
		switch {
		case name == corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Allocatable.SetMilli(r.MilliValue())
		case name == corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Allocatable.Set(r.Value())
		case name == corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Allocatable.Set(r.Value())
		case name == builder.ResourceGPUNvidia || name == builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Allocatable.Set(r.Value())
		case interconnectResource != "" && name == interconnectResource:
			node.Resources.GPUInterconnect.Allocatable.Set(r.Value())
		}
	}

	for name, r := range knode.Status.Capacity {
		switch {
		case name == corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Capacity.SetMilli(r.MilliValue())
		case name == corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Capacity.Set(r.Value())
		case name == corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Capacity.Set(r.Value())
		case name == builder.ResourceGPUNvidia || name == builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Capacity.Set(r.Value())
		case interconnectResource != "" && name == interconnectResource:
			node.Resources.GPUInterconnect.Capacity.Set(r.Value())
		}
	}
}

func nodeResetAllocated(node *v1.Node) {
	node.Resources.CPU.Quantity.Allocated = resource.NewMilliQuantity(0, resource.DecimalSI)
	node.Resources.GPU.Quantity.Allocated = resource.NewQuantity(0, resource.DecimalSI)
	node.Resources.Memory.Quantity.Allocated = resource.NewQuantity(0, resource.DecimalSI)
	node.Resources.EphemeralStorage.Allocated = resource.NewQuantity(0, resource.DecimalSI)
	node.Resources.VolumesAttached.Allocated = resource.NewQuantity(0, resource.DecimalSI)
	node.Resources.VolumesMounted.Allocated = resource.NewQuantity(0, resource.DecimalSI)
	node.Resources.GPUInterconnect.Allocated = resource.NewQuantity(0, resource.DecimalSI)
}

func addPodAllocatedResources(node *v1.Node, pod *corev1.Pod) {
	interconnectResource := corev1.ResourceName(node.Capabilities.InterconnectResourceName)
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			switch {
			case name == corev1.ResourceCPU:
				node.Resources.CPU.Quantity.Allocated.Add(quantity)
			case name == corev1.ResourceMemory:
				node.Resources.Memory.Quantity.Allocated.Add(quantity)
			case name == corev1.ResourceEphemeralStorage:
				node.Resources.EphemeralStorage.Allocated.Add(quantity)
			case name == builder.ResourceGPUNvidia || name == builder.ResourceGPUAMD:
				node.Resources.GPU.Quantity.Allocated.Add(quantity)
				// GPU overcommit is not allowed, if that happens something is terribly wrong with the inventory
			case interconnectResource != "" && name == interconnectResource:
				// interconnect is 1:1 with GPU per the spec; the device plugin
				// won't admit a pod that asks for more than is allocatable,
				// so straight accumulation matches the GPU pattern.
				node.Resources.GPUInterconnect.Allocated.Add(quantity)
			}
		}

		for _, vol := range pod.Spec.Volumes {
			if vol.EmptyDir == nil || vol.EmptyDir.Medium != corev1.StorageMediumMemory || vol.EmptyDir.SizeLimit == nil {
				continue
			}

			node.Resources.Memory.Quantity.Allocated.Add(*vol.EmptyDir.SizeLimit)
		}
	}
}

func subAllocatedNLZ(allocated *resource.Quantity, val resource.Quantity) {
	newVal := allocated.Value() - val.Value()
	if newVal < 0 {
		newVal = 0
	}

	allocated.Set(newVal)
}

func subPodAllocatedResources(node *v1.Node, pod *corev1.Pod) {
	interconnectResource := corev1.ResourceName(node.Capabilities.InterconnectResourceName)
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			switch {
			case name == corev1.ResourceCPU:
				subAllocatedNLZ(node.Resources.CPU.Quantity.Allocated, quantity)
			case name == corev1.ResourceMemory:
				subAllocatedNLZ(node.Resources.Memory.Quantity.Allocated, quantity)
			case name == corev1.ResourceEphemeralStorage:
				subAllocatedNLZ(node.Resources.EphemeralStorage.Allocated, quantity)
			case name == builder.ResourceGPUNvidia || name == builder.ResourceGPUAMD:
				subAllocatedNLZ(node.Resources.GPU.Quantity.Allocated, quantity)
			case interconnectResource != "" && name == interconnectResource:
				subAllocatedNLZ(node.Resources.GPUInterconnect.Allocated, quantity)
			}
		}

		for _, vol := range pod.Spec.Volumes {
			if vol.EmptyDir == nil || vol.EmptyDir.Medium != corev1.StorageMediumMemory || vol.EmptyDir.SizeLimit == nil {
				continue
			}

			subAllocatedNLZ(node.Resources.Memory.Quantity.Allocated, *vol.EmptyDir.SizeLimit)
		}
	}
}

func isLabelManaged(key string) bool {
	return strings.HasPrefix(key, builder.AkashManagedLabelName) || key == labelNvidiaComGPUPresent
}

func copyManagedLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))

	for key, val := range in {
		if !isLabelManaged(key) {
			continue
		}

		out[key] = val
	}

	return out
}

func removeManagedLabels(in map[string]string) map[string]string {
	out := make(map[string]string)

	for key, val := range in {
		if isLabelManaged(key) {
			continue
		}

		out[key] = val
	}

	return out
}

func isNodeReady(conditions []corev1.NodeCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == "True"
		}
	}

	return false
}

func generateLabels(cfg Config, knode *corev1.Node, node v1.Node, sc storageClasses) (map[string]string, v1.Node) {
	res := make(map[string]string)

	presentSc := make([]string, 0, len(sc))
	for name := range sc {
		presentSc = append(presentSc, name)
	}

	adjConfig := cfg.Copy()

	sort.Strings(presentSc)
	adjConfig.FilterOutStorageClasses(presentSc)

	isExcluded := !isNodeReady(knode.Status.Conditions) || knode.Spec.Unschedulable || adjConfig.Exclude.IsNodeExcluded(knode.Name)

	if isExcluded {
		node.Capabilities.StorageClasses = []string{}
		return res, node
	}

	res[builder.AkashManagedLabelName] = builder.ValTrue

	allowedSc := adjConfig.StorageClassesForNode(knode.Name)
	for _, class := range allowedSc {
		key := fmt.Sprintf("%s.class.%s", builder.AkashServiceCapabilityStorage, class)
		res[key] = "1"
	}

	node.Capabilities.StorageClasses = allowedSc

	for _, info := range node.Resources.GPU.Info {
		// nvidia device plugin requires nodes to be labeled with "nvidia.com/gpu.present"
		if info.Vendor == "nvidia" && res[labelNvidiaComGPUPresent] != "true" {
			res[labelNvidiaComGPUPresent] = "true"
		}

		key := fmt.Sprintf("%s.vendor.%s.model.%s", builder.AkashServiceCapabilityGPU, info.Vendor, info.Name)
		if val, exists := res[key]; exists {
			nval, _ := strconv.ParseUint(val, 10, 32)
			nval++
			res[key] = strconv.FormatUint(nval, 10)
		} else {
			res[key] = "1"
		}

		if info.MemorySize != "" {
			key := fmt.Sprintf("%s.ram.%s", key, info.MemorySize)
			if val, exists := res[key]; exists {
				nval, _ := strconv.ParseUint(val, 10, 32)
				nval++
				res[key] = strconv.FormatUint(nval, 10)
			} else {
				res[key] = "1"
			}
		}

		if info.Interface != "" {
			key := fmt.Sprintf("%s.interface.%s", key, ctypes.FilterGPUInterface(info.Interface))
			if val, exists := res[key]; exists {
				nval, _ := strconv.ParseUint(val, 10, 32)
				nval++
				res[key] = strconv.FormatUint(nval, 10)
			} else {
				res[key] = "1"
			}
		}
	}

	return res, node
}

func (dp *nodeDiscovery) parseCPUInfo(ctx context.Context) v1.CPUInfoS {
	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	cpus, err := dp.queryCPU(ctx)
	if err != nil {
		log.Error(err, "unable to query cpu")
		return v1.CPUInfoS{}
	}

	res := make(v1.CPUInfoS, 0, len(cpus.Processors))

	for _, c := range cpus.Processors {
		res = append(res, v1.CPUInfo{
			ID:     strconv.Itoa(c.ID),
			Vendor: c.Vendor,
			Model:  c.Model,
			Vcores: c.NumThreads,
		})
	}

	return res
}

// gpuInfoFromPCIDevice extracts a GPUInfo from a ghw pci.Device record,
// returning ok=false for devices outside the akash GPU registry so the
// caller can skip them when walking the full PCI bus.
//
// We deliberately do NOT filter by PCI class/subclass. Datacenter cards
// (A100, H100, etc.) enumerate as 03/02 ("3D controller") but consumer
// cards (RTX 4090/3090, A6000) enumerate as 03/00 ("VGA compatible
// controller") — the same class as the BMC VGA. The vendor+product
// registry lookup is what distinguishes them: BMC Matrox (vendor 102b)
// isn't in akash's GPU vendor registry, while NVIDIA/AMD compute and
// consumer cards both are. NVSwitch bridges (NVIDIA vendor, class 06)
// also fail the product lookup since their product IDs are not in the
// GPU model registry.
func gpuInfoFromPCIDevice(dev *pci.Device, registry RegistryGPUVendors) (v1.GPUInfo, bool) {
	if dev == nil || dev.Vendor == nil || dev.Product == nil {
		return v1.GPUInfo{}, false
	}

	vendor, exists := registry[dev.Vendor.ID]
	if !exists {
		return v1.GPUInfo{}, false
	}

	model, exists := vendor.Devices[dev.Product.ID]
	if !exists {
		return v1.GPUInfo{}, false
	}

	return v1.GPUInfo{
		Vendor:     vendor.Name,
		VendorID:   dev.Vendor.ID,
		Name:       model.Name,
		ModelID:    dev.Product.ID,
		Interface:  model.Interface,
		MemorySize: model.MemorySize,
	}, true
}

func (dp *nodeDiscovery) parseGPUInfo(ctx context.Context, info RegistryGPUVendors) v1.GPUInfoS {
	res := make(v1.GPUInfoS, 0)

	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	gpus, err := dp.queryGPU(ctx)
	if err != nil {
		log.Error(err, "unable to query gpu")
	} else if gpus != nil {
		for _, dev := range gpus.GraphicsCards {
			if ginfo, ok := gpuInfoFromPCIDevice(dev.DeviceInfo, info); ok {
				res = append(res, ginfo)
			}
		}
	}

	// Headless compute nodes (A100/H100 + NVIDIA GPU Operator) expose only
	// the BMC VGA under /sys/class/drm because the host kernel has no
	// nvidia driver — the driver lives in the operator's daemonset
	// container. Walk the PCI bus directly to find the compute cards.
	if len(res) == 0 {
		pciInfo, perr := dp.queryPCI(ctx)
		if perr != nil {
			log.Error(perr, "unable to query pci for gpu fallback")
		} else if pciInfo != nil {
			for _, dev := range pciInfo.Devices {
				if ginfo, ok := gpuInfoFromPCIDevice(dev, info); ok {
					res = append(res, ginfo)
				}
			}

			if len(res) > 0 {
				log.Info("discovered gpus via pci fallback", "count", len(res), "node", dp.name)
			}
		}
	}

	sort.Sort(res)

	return res
}
