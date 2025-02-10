package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/gpu"
	"github.com/jaypipes/ghw/pkg/memory"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	v1 "github.com/akash-network/akash-api/go/inventory/v1"
	types "github.com/akash-network/akash-api/go/node/types/v1beta3"

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
					Command: []string{
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

		select {
		case <-tctx.Done():
		}

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
					}
				}
			}

			readreq.resp <- resp
		}
	}
}

func isPodAllocated(status corev1.PodStatus) bool {
	isScheduled := false
	for _, condition := range status.Conditions {
		if (condition.Type == corev1.PodScheduled) && (condition.Status == corev1.ConditionTrue) {
			isScheduled = true
			break
		}
	}

	return isScheduled && (status.Phase == corev1.PodRunning || status.Phase == corev1.PodPending)
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

	node, err := dp.initNodeInfo(gpusIDs, knode)
	if err != nil {
		log.Error(err, "unable to init node info")
		return err
	}

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
							podsWatch.Stop()
							updateNodeInfo(obj, &node)
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

				signalState()
			case watch.Deleted:
				pod, exists := currPods[obj.Name]
				if !exists {
					log.Info("received pod delete event for item that does not exist. check node inventory logic, it's might have bug in it!")
					break
				}

				subPodAllocatedResources(&node, &pod)

				delete(currPods, obj.Name)

				signalState()
			}
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

func (dp *nodeDiscovery) initNodeInfo(gpusIDs RegistryGPUVendors, knode *corev1.Node) (v1.Node, error) {
	cpuInfo := dp.parseCPUInfo(dp.ctx)
	gpuInfo := dp.parseGPUInfo(dp.ctx, gpusIDs)

	res := v1.Node{
		Name: knode.Name,
		Resources: v1.NodeResources{
			CPU: v1.CPU{
				Quantity: v1.NewResourcePairMilli(0, 0, resource.DecimalSI),
				Info:     cpuInfo,
			},
			GPU: v1.GPU{
				Quantity: v1.NewResourcePair(0, 0, resource.DecimalSI),
				Info:     gpuInfo,
			},
			Memory: v1.Memory{
				Quantity: v1.NewResourcePair(0, 0, resource.DecimalSI),
				Info:     nil,
			},
			EphemeralStorage: v1.NewResourcePair(0, 0, resource.DecimalSI),
			VolumesAttached:  v1.NewResourcePair(0, 0, resource.DecimalSI),
			VolumesMounted:   v1.NewResourcePair(0, 0, resource.DecimalSI),
		},
	}

	updateNodeInfo(knode, &res)

	return res, nil
}

func updateNodeInfo(knode *corev1.Node, node *v1.Node) {
	for name, r := range knode.Status.Allocatable {
		switch name {
		case corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Allocatable.SetMilli(r.MilliValue())
		case corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Allocatable.Set(r.Value())
		case corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Allocatable.Set(r.Value())
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Allocatable.Set(r.Value())
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
}

func addPodAllocatedResources(node *v1.Node, pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			switch name {
			case corev1.ResourceCPU:
				node.Resources.CPU.Quantity.Allocated.Add(quantity)
			case corev1.ResourceMemory:
				node.Resources.Memory.Quantity.Allocated.Add(quantity)
			case corev1.ResourceEphemeralStorage:
				node.Resources.EphemeralStorage.Allocated.Add(quantity)
			case builder.ResourceGPUNvidia:
				fallthrough
			case builder.ResourceGPUAMD:
				node.Resources.GPU.Quantity.Allocated.Add(quantity)
				// GPU overcommit is not allowed, if that happens something is terribly wrong with the inventory
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

func subPodAllocatedResources(node *v1.Node, pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			rv := types.NewResourceValue(uint64(quantity.Value()))
			switch name {
			case corev1.ResourceCPU:
				node.Resources.CPU.Quantity.SubMilliNLZ(rv)
			case corev1.ResourceMemory:
				node.Resources.Memory.Quantity.SubNLZ(rv)
			case corev1.ResourceEphemeralStorage:
				node.Resources.EphemeralStorage.SubNLZ(rv)
			case builder.ResourceGPUNvidia:
				fallthrough
			case builder.ResourceGPUAMD:
				node.Resources.GPU.Quantity.SubNLZ(rv)
			}
		}

		for _, vol := range pod.Spec.Volumes {
			if vol.EmptyDir == nil || vol.EmptyDir.Medium != corev1.StorageMediumMemory || vol.EmptyDir.SizeLimit == nil {
				continue
			}

			rv := types.NewResourceValue(uint64((*vol.EmptyDir.SizeLimit).Value()))

			node.Resources.Memory.Quantity.SubNLZ(rv)
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

func (dp *nodeDiscovery) parseGPUInfo(ctx context.Context, info RegistryGPUVendors) v1.GPUInfoS {
	res := make(v1.GPUInfoS, 0)

	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	gpus, err := dp.queryGPU(ctx)
	if err != nil {
		log.Error(err, "unable to query gpu")
		return res
	}

	if gpus == nil {
		return res
	}

	for _, dev := range gpus.GraphicsCards {
		dinfo := dev.DeviceInfo
		if dinfo == nil {
			continue
		}

		vinfo := dinfo.Vendor
		pinfo := dinfo.Product
		if vinfo == nil || pinfo == nil {
			continue
		}

		vendor, exists := info[vinfo.ID]
		if !exists {
			continue
		}

		model, exists := vendor.Devices[pinfo.ID]
		if !exists {
			continue
		}

		res = append(res, v1.GPUInfo{
			Vendor:     vendor.Name,
			VendorID:   dev.DeviceInfo.Vendor.ID,
			Name:       model.Name,
			ModelID:    dev.DeviceInfo.Product.ID,
			Interface:  model.Interface,
			MemorySize: model.MemorySize,
		})
	}

	sort.Sort(res)

	return res
}
