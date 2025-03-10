package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
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

	"github.com/akash-network/provider/cluster/kube/builder"
	ctypes "github.com/akash-network/provider/cluster/types/v1beta3"
	"github.com/akash-network/provider/tools/fromctx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
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

	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	select {
	case <-ctx.Done():
		log.Error(ctx.Err(), "context cancelled while querying GPU")
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		log.Error(dp.ctx.Err(), "node discovery context cancelled while querying GPU")
		return nil, dp.ctx.Err()
	case <-rctx.Done():
		log.Error(rctx.Err(), "timeout while querying GPU (5s timeout)")
		return nil, rctx.Err()
	case dp.readch <- dpReadReq{
		ctx:  rctx,
		op:   dpReqGPU,
		resp: respch,
	}:
		log.V(2).Info("sent GPU query request")
	}

	select {
	case <-ctx.Done():
		log.Error(ctx.Err(), "context cancelled while waiting for GPU response")
		return nil, ctx.Err()
	case <-dp.ctx.Done():
		log.Error(dp.ctx.Err(), "node discovery context cancelled while waiting for GPU response")
		return nil, dp.ctx.Err()
	case resp := <-respch:
		if resp.data == nil {
			log.Error(resp.err, "received nil data in GPU response")
			return nil, resp.err
		}
		log.V(2).Info("successfully received GPU info")
		return resp.data.(*gpu.Info), resp.err
	}
}

func (dp *nodeDiscovery) queryNvidiaDevicePlugin(ctx context.Context) (*gpu.Info, error) {
	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	// Common base directories to search
	baseDirs := []string{
		"/var/lib",
		"/data",
		"/opt",
		"/var/run",
		"/run",
		"/tmp",
	}

	// Common subdirectories to look for
	subDirs := []string{
		"kubelet/device-plugins",
		"kubelet/plugins_registry",
		"kubelet/plugins",
		"device-plugins",
		"plugins_registry",
		"plugins",
	}

	// Common socket names
	socketNames := []string{
		"nvidia-gpu.sock",
		"nvidia.sock",
	}

	// Create a channel to receive results
	resultChan := make(chan string, 1)
	errorChan := make(chan error, 1)
	doneChan := make(chan struct{})

	// Create a context with timeout for the entire search
	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Start parallel search
	var wg sync.WaitGroup
	for _, baseDir := range baseDirs {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			// First try direct paths
			for _, subDir := range subDirs {
				for _, socketName := range socketNames {
					path := filepath.Join(dir, subDir, socketName)
					if _, err := os.Stat(path); err == nil {
						select {
						case resultChan <- path:
						case <-searchCtx.Done():
						}
						return
					}
				}
			}

			// Then try recursive search
			err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // Skip errors
				}
				if info.IsDir() {
					return nil // Continue walking
				}
				if info.Mode()&os.ModeSocket != 0 {
					for _, socketName := range socketNames {
						if strings.HasSuffix(path, socketName) {
							select {
							case resultChan <- path:
							case <-searchCtx.Done():
								return fmt.Errorf("search cancelled")
							}
							return fmt.Errorf("found socket") // Stop walking
						}
					}
				}
				return nil
			})
			if err != nil && err.Error() != "found socket" {
				select {
				case errorChan <- err:
				case <-searchCtx.Done():
				}
			}
		}(baseDir)
	}

	// Wait for all searches to complete
	go func() {
		wg.Wait()
		close(doneChan)
	}()

	// Wait for result or timeout
	var socketPath string
	select {
	case socketPath = <-resultChan:
		log.Info("Found NVIDIA device plugin socket", "path", socketPath)
	case err := <-errorChan:
		log.Error(err, "Error during socket search")
		return nil, fmt.Errorf("error searching for NVIDIA device plugin socket: %v", err)
	case <-searchCtx.Done():
		log.Error(nil, "Timeout while searching for NVIDIA device plugin socket")
		return nil, fmt.Errorf("timeout searching for NVIDIA device plugin socket")
	case <-doneChan:
		log.Error(nil, "NVIDIA device plugin socket not found")
		return nil, fmt.Errorf("NVIDIA device plugin socket not found")
	}

	// Create a gRPC connection
	conn, err := grpc.Dial(
		socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)
	if err != nil {
		log.Error(err, "Failed to connect to NVIDIA device plugin")
		return nil, err
	}
	defer conn.Close()

	// Create a device plugin client
	client := pluginapi.NewDevicePluginClient(conn)

	// Create a context with timeout
	ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// List available devices
	resp, err := client.ListAndWatch(ctx, &pluginapi.Empty{})
	if err != nil {
		log.Error(err, "Failed to list devices from NVIDIA device plugin")
		return nil, err
	}

	// Get the first response
	response, err := resp.Recv()
	if err != nil {
		log.Error(err, "Failed to receive device list from NVIDIA device plugin")
		return nil, err
	}

	// Log device information
	for _, device := range response.Devices {
		health := "Healthy"
		if device.Health != pluginapi.Healthy {
			health = "Unhealthy"
		}
		log.Info("NVIDIA GPU Device",
			"id", device.ID,
			"health", health,
			"topology", device.Topology,
		)
	}

	// Convert device plugin response to gpu.Info
	gpuInfo := &gpu.Info{
		GraphicsCards: make([]*gpu.GraphicsCard, 0, len(response.Devices)),
	}

	for _, device := range response.Devices {
		// Extract GPU information from device properties
		var vendorID, productID string
		var gpuName string

		// Parse device properties to get vendor and product IDs
		for _, prop := range device.Properties {
			switch prop.Name {
			case "vendor":
				vendorID = fmt.Sprintf("%x", prop.Value)
			case "product":
				productID = fmt.Sprintf("%x", prop.Value)
			case "name":
				gpuName = prop.Value
			}
		}

		// If we couldn't get the vendor ID from properties, use NVIDIA's ID
		if vendorID == "" {
			vendorID = "10de"
		}

		// Create a graphics card entry with actual GPU information
		card := &gpu.GraphicsCard{
			DeviceInfo: &gpu.DeviceInfo{
				Vendor: &gpu.VendorInfo{
					ID:   vendorID,
					Name: "nvidia",
				},
				Product: &gpu.ProductInfo{
					ID:   productID,
					Name: gpuName,
				},
			},
		}

		// Add additional device properties as attributes
		for _, prop := range device.Properties {
			if prop.Name != "vendor" && prop.Name != "product" && prop.Name != "name" {
				card.DeviceInfo.Product.Attributes = append(card.DeviceInfo.Product.Attributes, &gpu.ProductAttribute{
					Name:  prop.Name,
					Value: prop.Value,
				})
			}
		}

		gpuInfo.GraphicsCards = append(gpuInfo.GraphicsCards, card)
	}

	log.V(2).Info("Successfully queried NVIDIA GPUs", "count", len(gpuInfo.GraphicsCards))
	return gpuInfo, nil
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
	for _, condition := range status.Conditions {
		if condition.Type == corev1.PodScheduled {
			return (condition.Status == corev1.ConditionTrue) && (status.Phase == corev1.PodRunning)
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
							// podsWatch.Stop()
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

func (dp *nodeDiscovery) initNodeInfo(gpusIDs RegistryGPUVendors, knode *corev1.Node) (v1.Node, error) {
	cpuInfo := dp.parseCPUInfo(dp.ctx)
	gpuInfo := dp.parseGPUInfo(dp.ctx, gpusIDs)

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
			node.Resources.EphemeralStorage.Allocated.Set(r.Value())
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Allocatable.Set(r.Value())
		}
	}

	for name, r := range knode.Status.Capacity {
		switch name {
		case corev1.ResourceCPU:
			node.Resources.CPU.Quantity.Capacity.SetMilli(r.MilliValue())
		case corev1.ResourceMemory:
			node.Resources.Memory.Quantity.Capacity.Set(r.Value())
		case corev1.ResourceEphemeralStorage:
			node.Resources.EphemeralStorage.Capacity.Set(r.Value())
		case builder.ResourceGPUNvidia:
			fallthrough
		case builder.ResourceGPUAMD:
			node.Resources.GPU.Quantity.Capacity.Set(r.Value())
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

func subAllocatedNLZ(allocated *resource.Quantity, val resource.Quantity) {
	newVal := allocated.Value() - val.Value()
	if newVal < 0 {
		newVal = 0
	}

	allocated.Set(newVal)
}

func subPodAllocatedResources(node *v1.Node, pod *corev1.Pod) {
	for _, container := range pod.Spec.Containers {
		for name, quantity := range container.Resources.Requests {
			switch name {
			case corev1.ResourceCPU:
				subAllocatedNLZ(node.Resources.CPU.Quantity.Allocated, quantity)
			case corev1.ResourceMemory:
				subAllocatedNLZ(node.Resources.Memory.Quantity.Allocated, quantity)
			case corev1.ResourceEphemeralStorage:
				subAllocatedNLZ(node.Resources.EphemeralStorage.Allocated, quantity)
			case builder.ResourceGPUNvidia:
				fallthrough
			case builder.ResourceGPUAMD:
				subAllocatedNLZ(node.Resources.GPU.Quantity.Allocated, quantity)
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

func (dp *nodeDiscovery) parseGPUInfo(ctx context.Context, info RegistryGPUVendors) v1.GPUInfoS {
	res := make(v1.GPUInfoS, 0)
	log := fromctx.LogrFromCtx(ctx).WithName("node.monitor")

	// Query NVIDIA device plugin first
	nvidiaInfo, err := dp.queryNvidiaDevicePlugin(ctx)
	if err != nil {
		log.Error(err, "failed to query NVIDIA device plugin")
	} else if nvidiaInfo != nil {
		// Process NVIDIA GPUs from device plugin
		for _, card := range nvidiaInfo.GraphicsCards {
			if card.DeviceInfo == nil || card.DeviceInfo.Vendor == nil || card.DeviceInfo.Product == nil {
				continue
			}

			vendor, exists := info[card.DeviceInfo.Vendor.ID]
			if !exists {
				continue
			}

			model, exists := vendor.Devices[card.DeviceInfo.Product.ID]
			if !exists {
				continue
			}

			// Create base GPU info
			gpuInfo := v1.GPUInfo{
				Vendor:     vendor.Name,
				VendorID:   card.DeviceInfo.Vendor.ID,
				Name:       model.Name,
				ModelID:    card.DeviceInfo.Product.ID,
				Interface:  model.Interface,
				MemorySize: model.MemorySize,
			}

			// Add additional attributes from nvidia-smi
			if card.DeviceInfo.Product.Name != "" {
				gpuInfo.Attributes = append(gpuInfo.Attributes, v1.Attribute{
					Key:   "gpu_name",
					Value: card.DeviceInfo.Product.Name,
				})
			}

			// Add memory information
			if card.DeviceInfo.Product.MemorySize != "" {
				gpuInfo.Attributes = append(gpuInfo.Attributes, v1.Attribute{
					Key:   "memory_size",
					Value: card.DeviceInfo.Product.MemorySize,
				})
			}

			res = append(res, gpuInfo)
		}
	}

	// Then query GPU info as before for non-NVIDIA GPUs
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
